package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/AnishMulay/sandstore/internal/cluster_service"
	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	ps "github.com/AnishMulay/sandstore/internal/server"
)

// readDirResponse mirrors the shape returned by the POSIX server for ReadDir.
type readDirResponse struct {
	Entries []pms.DirEntry `json:"entries"`
	Cookie  int            `json:"cookie"`
	EOF     bool           `json:"eof"`
}

// readDirPlusResponse mirrors the shape returned by the POSIX server for ReadDirPlus.
type readDirPlusResponse struct {
	Entries []pms.DirEntryPlus `json:"entries"`
	Cookie  int                `json:"cookie"`
	EOF     bool               `json:"eof"`
}

type storyRunner struct {
	ctx        context.Context
	comm       *grpccomm.GRPCCommunicator
	serverAddr string
	pause      time.Duration
}

func main() {
	serverAddr := "127.0.0.1:9001" // Default to first node; client will follow leader hints.
	uid := uint32(1000)
	gid := uint32(1000)

	ls := locallog.NewLocalDiscLogService("./run/client/logs", "posix-client", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)
	registerPayloads(comm)

	r := &storyRunner{
		ctx:        context.Background(),
		comm:       comm,
		serverAddr: serverAddr,
		pause:      time.Second,
	}

	log.Println("=== POSIX integration story starting ===")
	time.Sleep(r.pause) // Let the cluster settle before the first request.

	rootID := r.lookupPath("/")
	log.Printf("Root inode discovered: %s\n", rootID)

	info := decode[pms.FileSystemInfo](r, ps.MsgFsInfo, ps.FsInfoRequest{})
	log.Printf("FS Info: chunkSize=%d maxFile=%d maxName=%d\n", info.ChunkSize, info.MaxFileSize, info.MaxFilenameSize)

	stats := decode[pms.FileSystemStats](r, ps.MsgFsStat, ps.FsStatRequest{})
	log.Printf("FS Stats: usedInodes=%d totalInodes=%d blockSize=%d\n", stats.UsedInodes, stats.TotalInodes, stats.BlockSize)
	log.Println("[PASS] Fetched filesystem info and stats")

	r.pauseNow()

	// Workspace layout: /home, /home/alice, /tmp
	home := r.mkdir(rootID, "home", 0755, uid, gid)
	alice := r.mkdir(home.InodeID, "alice", 0755, uid, gid)
	tmp := r.mkdir(rootID, "tmp", 0755, uid, gid)
	log.Printf("Workspace created: home=%s alice=%s tmp=%s\n", home.InodeID, alice.InodeID, tmp.InodeID)
	log.Println("[PASS] Created workspace directories")

	// ReadDir with pagination to prove listing works even with unordered maps.
	rootEntries := r.collectDir(rootID, 1)
	log.Printf("Root entries: %v\n", namesFromEntries(rootEntries))
	requireContains(rootEntries, "home", "tmp")

	homePlus := r.collectDirPlus(home.InodeID, 5)
	log.Printf("/home entries (+attrs): %v\n", namesFromDirPlus(homePlus))
	requireContainsPlus(homePlus, "alice")
	log.Println("[PASS] Directory listings and pagination verified")

	r.pauseNow()

	// Create a file and inspect it.
	file := r.create(alice.InodeID, "readme.txt", 0644, uid, gid)
	log.Printf("Created file readme.txt inode=%s\n", file.InodeID)

	attr := r.getAttr(file.InodeID)
	log.Printf("Initial attrs: mode=%o size=%d uid=%d gid=%d\n", attr.Mode, attr.Size, attr.UID, attr.GID)
	log.Println("[PASS] File creation and initial attributes verified")

	r.access(file.InodeID, uid, gid, 06) // rw for owner

	// Tighten permissions and verify.
	mode := uint32(0600)
	r.setAttr(file.InodeID, &mode, nil, nil, nil, nil)
	attr = r.getAttr(file.InodeID)
	log.Printf("After chmod: mode=%o\n", attr.Mode)
	log.Println("[PASS] Permissions tightened and revalidated")

	r.pauseNow()

	// Content lifecycle: write, partial overwrite, append, EOF read.
	initialData := []byte("Hello from sandstore")
	r.write(file.InodeID, 0, initialData)
	readFull := r.readBytes(file.InodeID, 0, int64(len(initialData)+4))
	ensureBytes("initial readback", initialData, readFull)

	overlay := []byte("POSIX")
	r.write(file.InodeID, 6, overlay)
	expected := append([]byte{}, initialData...)
	copy(expected[6:], overlay)
	readOverlay := r.readBytes(file.InodeID, 0, int64(len(expected)))
	ensureBytes("partial overwrite", expected, readOverlay)

	appendData := []byte("!!!")
	r.write(file.InodeID, int64(len(expected)), appendData)
	expected = append(expected, appendData...)
	finalRead := r.readBytes(file.InodeID, 0, int64(len(expected)+8))
	ensureBytes("final content", expected, finalRead)

	emptyTail := r.readBytes(file.InodeID, int64(len(expected)+32), 8)
	if len(emptyTail) != 0 {
		log.Fatalf("Expected empty tail read, got %d bytes", len(emptyTail))
	}
	log.Println("[PASS] Read/write lifecycle verified")

	// Access checks: successful for owner, denied for other user.
	r.access(file.InodeID, uid, gid, 04)
	r.expectFailure(ps.MsgAccess, ps.AccessRequest{
		InodeID:    file.InodeID,
		UID:        2001,
		GID:        2001,
		AccessMask: 04,
	}, "permission should be denied for other user")
	log.Println("[PASS] Access checks verified (owner allowed, other denied)")

	r.pauseNow()

	// Lookup both ways.
	lookupID := r.lookup(alice.InodeID, "readme.txt")
	if lookupID != file.InodeID {
		log.Fatalf("Lookup mismatch: expected %s got %s", file.InodeID, lookupID)
	}
	pathID := r.lookupPath("/home/alice/readme.txt")
	if pathID != file.InodeID {
		log.Fatalf("LookupPath mismatch: expected %s got %s", file.InodeID, pathID)
	}
	log.Println("[PASS] Lookup and LookupPath resolved correctly")

	// Directory listings that include the file.
	alicePlus := r.collectDirPlus(alice.InodeID, 2)
	requireContainsPlus(alicePlus, "readme.txt")
	log.Println("[PASS] Directory listings include created file")

	r.pauseNow()

	// Rename into /tmp and validate redirects and directory emptiness checks.
	r.rename(alice.InodeID, "readme.txt", tmp.InodeID, "moved.txt")
	movedID := r.lookupPath("/tmp/moved.txt")
	if movedID != file.InodeID {
		log.Fatalf("Rename changed inode: expected %s got %s", file.InodeID, movedID)
	}
	r.expectFailure(ps.MsgLookupPath, ps.LookupPathRequest{Path: "/home/alice/readme.txt"}, "old path should be gone after rename")
	log.Println("[PASS] Rename succeeded and old path invalidated")

	// Rmdir should fail while /tmp contains the file.
	r.expectFailure(ps.MsgRmdir, ps.RmdirRequest{ParentID: rootID, Name: "tmp"}, "rmdir on non-empty dir should fail")

	tmpPlus := r.collectDirPlus(tmp.InodeID, 2)
	requireContainsPlus(tmpPlus, "moved.txt")
	log.Println("[PASS] Non-empty rmdir rejected and contents confirmed")

	// Clean up file and directories.
	r.remove(tmp.InodeID, "moved.txt")
	r.rmdir(rootID, "tmp")
	r.rmdir(home.InodeID, "alice")
	r.rmdir(rootID, "home")
	log.Println("[PASS] Cleanup completed")

	finalStats := decode[pms.FileSystemStats](r, ps.MsgFsStat, ps.FsStatRequest{})
	log.Printf("Final FS Stats: usedInodes=%d totalInodes=%d\n", finalStats.UsedInodes, finalStats.TotalInodes)

	log.Println("=== POSIX integration story complete (all steps passed) ===")
}

func (r *storyRunner) pauseNow() {
	time.Sleep(r.pause)
}

func (r *storyRunner) send(msgType string, payload any, allowNonOK bool) *communication.Response {
	msg := communication.Message{From: "client", Type: msgType, Payload: payload}
	addr := r.serverAddr

	for attempt := 0; attempt < 2; attempt++ {
		log.Printf("-> %s to %s", msgType, addr)
		resp, err := r.comm.Send(r.ctx, addr, msg)
		if err != nil {
			log.Fatalf("Comm Error for %s: %v", msgType, err)
		}

		if resp.Code == communication.CodeOK {
			r.serverAddr = addr
			return resp
		}

		if attempt == 0 {
			if leaderAddr := extractLeaderAddr(resp.Body); leaderAddr != "" {
				log.Printf("   Not the leader. Retrying on hinted leader: %s", leaderAddr)
				addr = leaderAddr
				continue
			}

			var leaderInfo cluster_service.Node
			if err := json.Unmarshal(resp.Body, &leaderInfo); err == nil && leaderInfo.Address != "" {
				log.Printf("   Not the leader. Retrying on hinted leader: %s", leaderInfo.Address)
				addr = leaderInfo.Address
				continue
			}
		}

		if !allowNonOK {
			log.Fatalf("Server Error for %s (from %s): %s", msgType, addr, strings.TrimSpace(string(resp.Body)))
		}
		r.serverAddr = addr
		return resp
	}

	log.Fatalf("Exhausted retries for %s", msgType)
	return nil
}

func decode[T any](r *storyRunner, msgType string, payload any) T {
	resp := r.send(msgType, payload, false)
	var out T
	if len(resp.Body) == 0 {
		return out
	}
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		log.Fatalf("Failed to decode response for %s: %v (body=%s)", msgType, err, string(resp.Body))
	}
	return out
}

func (r *storyRunner) expectFailure(msgType string, payload any, context string) {
	resp := r.send(msgType, payload, true)
	if resp.Code == communication.CodeOK {
		log.Fatalf("Expected failure for %s (%s) but got OK", msgType, context)
	}
	log.Printf("Expected failure for %s: %s (code=%s)", msgType, strings.TrimSpace(string(resp.Body)), resp.Code)
}

func (r *storyRunner) lookup(parentID, name string) string {
	return decode[string](r, ps.MsgLookup, ps.LookupRequest{ParentID: parentID, Name: name})
}

func (r *storyRunner) lookupPath(path string) string {
	return decode[string](r, ps.MsgLookupPath, ps.LookupPathRequest{Path: path})
}

func (r *storyRunner) mkdir(parentID, name string, mode, uid, gid uint32) *pms.Inode {
	return decode[*pms.Inode](r, ps.MsgMkdir, ps.MkdirRequest{
		ParentID: parentID,
		Name:     name,
		Mode:     mode,
		UID:      uid,
		GID:      gid,
	})
}

func (r *storyRunner) create(parentID, name string, mode, uid, gid uint32) *pms.Inode {
	return decode[*pms.Inode](r, ps.MsgCreate, ps.CreateRequest{
		ParentID: parentID,
		Name:     name,
		Mode:     mode,
		UID:      uid,
		GID:      gid,
	})
}

func (r *storyRunner) getAttr(inodeID string) *pms.Attributes {
	return decode[*pms.Attributes](r, ps.MsgGetAttr, ps.GetAttrRequest{InodeID: inodeID})
}

func (r *storyRunner) setAttr(inodeID string, mode, uid, gid *uint32, atime, mtime *int64) *pms.Attributes {
	return decode[*pms.Attributes](r, ps.MsgSetAttr, ps.SetAttrRequest{
		InodeID: inodeID,
		Mode:    mode,
		UID:     uid,
		GID:     gid,
		ATime:   atime,
		MTime:   mtime,
	})
}

func (r *storyRunner) access(inodeID string, uid, gid, mask uint32) {
	r.send(ps.MsgAccess, ps.AccessRequest{
		InodeID:    inodeID,
		UID:        uid,
		GID:        gid,
		AccessMask: mask,
	}, false)
}

func (r *storyRunner) readBytes(inodeID string, offset, length int64) []byte {
	data := decode[[]byte](r, ps.MsgRead, ps.ReadRequest{
		InodeID: inodeID,
		Offset:  offset,
		Length:  length,
	})
	return data
}

func (r *storyRunner) write(inodeID string, offset int64, data []byte) int64 {
	return decode[int64](r, ps.MsgWrite, ps.WriteRequest{
		InodeID: inodeID,
		Offset:  offset,
		Data:    data,
	})
}

func (r *storyRunner) rename(srcParentID, srcName, dstParentID, dstName string) {
	r.send(ps.MsgRename, ps.RenameRequest{
		SrcParentID: srcParentID,
		SrcName:     srcName,
		DstParentID: dstParentID,
		DstName:     dstName,
	}, false)
}

func (r *storyRunner) remove(parentID, name string) {
	r.send(ps.MsgRemove, ps.RemoveRequest{ParentID: parentID, Name: name}, false)
}

func (r *storyRunner) rmdir(parentID, name string) {
	r.send(ps.MsgRmdir, ps.RmdirRequest{ParentID: parentID, Name: name}, false)
}

func (r *storyRunner) collectDir(inodeID string, maxEntries int) []pms.DirEntry {
	var entries []pms.DirEntry
	cookie := 0

	for {
		page := decode[readDirResponse](r, ps.MsgReadDir, ps.ReadDirRequest{
			InodeID:    inodeID,
			Cookie:     cookie,
			MaxEntries: maxEntries,
		})
		entries = append(entries, page.Entries...)
		cookie = page.Cookie
		if page.EOF {
			break
		}
		r.pauseNow()
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func (r *storyRunner) collectDirPlus(inodeID string, maxEntries int) []pms.DirEntryPlus {
	var entries []pms.DirEntryPlus
	cookie := 0

	for {
		page := decode[readDirPlusResponse](r, ps.MsgReadDirPlus, ps.ReadDirPlusRequest{
			InodeID:    inodeID,
			Cookie:     cookie,
			MaxEntries: maxEntries,
		})
		entries = append(entries, page.Entries...)
		cookie = page.Cookie
		if page.EOF {
			break
		}
		r.pauseNow()
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func registerPayloads(comm *grpccomm.GRPCCommunicator) {
	comm.RegisterPayloadType(ps.MsgGetAttr, reflect.TypeOf(ps.GetAttrRequest{}))
	comm.RegisterPayloadType(ps.MsgSetAttr, reflect.TypeOf(ps.SetAttrRequest{}))
	comm.RegisterPayloadType(ps.MsgLookup, reflect.TypeOf(ps.LookupRequest{}))
	comm.RegisterPayloadType(ps.MsgLookupPath, reflect.TypeOf(ps.LookupPathRequest{}))
	comm.RegisterPayloadType(ps.MsgAccess, reflect.TypeOf(ps.AccessRequest{}))
	comm.RegisterPayloadType(ps.MsgRead, reflect.TypeOf(ps.ReadRequest{}))
	comm.RegisterPayloadType(ps.MsgWrite, reflect.TypeOf(ps.WriteRequest{}))
	comm.RegisterPayloadType(ps.MsgCreate, reflect.TypeOf(ps.CreateRequest{}))
	comm.RegisterPayloadType(ps.MsgMkdir, reflect.TypeOf(ps.MkdirRequest{}))
	comm.RegisterPayloadType(ps.MsgRemove, reflect.TypeOf(ps.RemoveRequest{}))
	comm.RegisterPayloadType(ps.MsgRmdir, reflect.TypeOf(ps.RmdirRequest{}))
	comm.RegisterPayloadType(ps.MsgRename, reflect.TypeOf(ps.RenameRequest{}))
	comm.RegisterPayloadType(ps.MsgReadDir, reflect.TypeOf(ps.ReadDirRequest{}))
	comm.RegisterPayloadType(ps.MsgReadDirPlus, reflect.TypeOf(ps.ReadDirPlusRequest{}))
	comm.RegisterPayloadType(ps.MsgFsStat, reflect.TypeOf(ps.FsStatRequest{}))
	comm.RegisterPayloadType(ps.MsgFsInfo, reflect.TypeOf(ps.FsInfoRequest{}))
}

// Utility helpers and assertions.

func namesFromEntries(entries []pms.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}

func namesFromDirPlus(entries []pms.DirEntryPlus) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names
}

func requireContains(entries []pms.DirEntry, expected ...string) {
	set := make(map[string]bool)
	for _, e := range entries {
		set[e.Name] = true
	}
	for _, name := range expected {
		if !set[name] {
			log.Fatalf("Expected directory to contain %s", name)
		}
	}
}

func requireContainsPlus(entries []pms.DirEntryPlus, expected ...string) {
	set := make(map[string]bool)
	for _, e := range entries {
		set[e.Name] = true
	}
	for _, name := range expected {
		if !set[name] {
			log.Fatalf("Expected directory to contain %s", name)
		}
	}
}

func ensureBytes(label string, expected, actual []byte) {
	if !bytes.Equal(expected, actual) {
		log.Fatalf("%s mismatch.\nexpected: %q\ngot     : %q", label, string(expected), string(actual))
	}
}

// extractLeaderAddr tries to pull a leader address out of an error body.
// It supports the raft error string format: "node is not the leader. leader is X at Y".
func extractLeaderAddr(body []byte) string {
	s := string(body)
	const marker = " at "
	if idx := strings.LastIndex(s, marker); idx != -1 {
		leaderAddr := strings.TrimSpace(s[idx+len(marker):])
		if leaderAddr != "" {
			return leaderAddr
		}
	}
	return ""
}
