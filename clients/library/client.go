package sandlib

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	pathpkg "path"
	filepathpkg "path/filepath"
	"strings"

	"github.com/AnishMulay/sandstore/internal/communication"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	ps "github.com/AnishMulay/sandstore/internal/server"
)

const firstUserFD uint64 = 3
const maxBufferSize = 2 * 1024 * 1024

// NewSandstoreClient constructs a client bound to one server address and
// initializes an empty descriptor table.
//
// It performs no network calls and acquires no locks.
func NewSandstoreClient(serverAddr string, comm *grpccomm.GRPCCommunicator) *SandstoreClient {
	return &SandstoreClient{
		ServerAddr: serverAddr,
		Comm:       comm,
		OpenFiles:  make(map[uint64]*SandstoreFD),
	}
}

// Open resolves path to an inode and returns a process-local file descriptor.
//
// Behavior:
// - Uses LookupPath first.
// - If not found and O_CREATE is set, issues Create then retries lookup on
//   already-exists races.
// - Inserts a new SandstoreFD with Offset 0 and an empty write buffer.
// - O_EXCL is not currently enforced client-side; an existing path is opened
//   successfully when lookup returns OK.
//
// Thread-safety:
// - Performs network RPCs without holding TableMu.
// - Acquires TableMu write lock only inside addFD when mutating OpenFiles.
//
// Consistency:
// - LookupPath is a metadata read from the contacted server.
// - Create is an atomic, Raft-replicated metadata mutation before success.
func (c *SandstoreClient) Open(path string, mode int) (int, error) {
	if c == nil {
		return 0, fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return 0, fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return 0, fmt.Errorf("sandstore server address is empty")
	}

	cleanPath, err := normalizePath(path)
	if err != nil {
		return 0, err
	}

	inodeID, lookupResp, err := c.lookupPath(cleanPath)
	if err != nil {
		return 0, err
	}

	switch lookupResp.Code {
	case communication.CodeOK:
		return c.addFD(inodeID, cleanPath, mode)
	case communication.CodeNotFound:
		if mode&os.O_CREATE == 0 {
			return 0, responseError("open", cleanPath, lookupResp)
		}
	default:
		return 0, responseError("open", cleanPath, lookupResp)
	}

	createResp, err := c.createPath(cleanPath, mode)
	if err != nil {
		return 0, err
	}

	switch createResp.Code {
	case communication.CodeOK:
		inodeID, decodeErr := decodeInodeID(createResp.Body)
		if decodeErr != nil {
			return 0, decodeErr
		}
		return c.addFD(inodeID, cleanPath, mode)
	case communication.CodeAlreadyExists:
		inodeID, retryResp, retryErr := c.lookupPath(cleanPath)
		if retryErr != nil {
			return 0, retryErr
		}
		if retryResp.Code != communication.CodeOK {
			return 0, responseError("open", cleanPath, retryResp)
		}
		return c.addFD(inodeID, cleanPath, mode)
	default:
		return 0, responseError("create", cleanPath, createResp)
	}
}

// Read returns up to n bytes from the file's current offset and advances the
// local offset by the bytes actually returned.
//
// Thread-safety:
// - Acquires TableMu read lock to resolve fd -> SandstoreFD.
// - Acquires the file-level mutex (SandstoreFD.Mu) for the full operation.
//
// Consistency:
// - Uses MsgRead, which is a local server read path and therefore eventually
//   consistent with the latest committed cluster state.
func (c *SandstoreClient) Read(fd int, n int) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return nil, fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return nil, fmt.Errorf("sandstore server address is empty")
	}
	if fd < 0 {
		return nil, fmt.Errorf("bad file descriptor")
	}
	if n < 0 {
		return nil, fmt.Errorf("invalid read length %d", n)
	}

	c.TableMu.RLock()
	fileStruct := c.OpenFiles[uint64(fd)]
	c.TableMu.RUnlock()

	if fileStruct == nil {
		return nil, fmt.Errorf("bad file descriptor")
	}

	fileStruct.Mu.Lock()
	defer fileStruct.Mu.Unlock()

	if fileStruct.Mode&os.O_WRONLY != 0 && fileStruct.Mode&os.O_RDWR == 0 {
		return nil, fmt.Errorf("file not open for reading")
	}

	resp, err := c.send(ps.MsgRead, ps.ReadRequest{
		InodeID: fileStruct.InodeID,
		Offset:  fileStruct.Offset,
		Length:  int64(n),
	})
	if err != nil {
		return nil, fmt.Errorf("read %q failed: %w", fileStruct.FilePath, err)
	}
	if resp.Code != communication.CodeOK {
		return nil, responseError("read", fileStruct.FilePath, resp)
	}

	var data []byte
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &data); err != nil {
			return nil, fmt.Errorf("failed to decode read response for %q: %w", fileStruct.FilePath, err)
		}
	}

	fileStruct.Offset += int64(len(data))
	return data, nil
}

// Write appends data to the per-fd client buffer and advances the logical
// offset.
//
// Behavior:
// - If appending would exceed maxBufferSize, flushes the existing buffer first
//   with MsgWrite at (Offset - len(Buffer)).
// - Newly provided data is buffered after any overflow flush.
// - Returns len(data) on success.
//
// Thread-safety:
// - Acquires TableMu read lock to resolve fd.
// - Acquires SandstoreFD.Mu for buffer and offset mutation.
//
// Consistency:
// - Buffered bytes are not persisted until overflow flush, Fsync, or Close.
// - Each MsgWrite flush is a synchronous, Raft-replicated write before return.
func (c *SandstoreClient) Write(fd int, data []byte) (int, error) {
	if c == nil {
		return 0, fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return 0, fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return 0, fmt.Errorf("sandstore server address is empty")
	}
	if fd < 0 {
		return 0, fmt.Errorf("bad file descriptor")
	}

	c.TableMu.RLock()
	fileStruct := c.OpenFiles[uint64(fd)]
	c.TableMu.RUnlock()

	if fileStruct == nil {
		return 0, fmt.Errorf("bad file descriptor")
	}

	fileStruct.Mu.Lock()
	defer fileStruct.Mu.Unlock()

	if fileStruct.Mode&os.O_WRONLY == 0 && fileStruct.Mode&os.O_RDWR == 0 {
		return 0, fmt.Errorf("file not open for writing")
	}

	if len(fileStruct.Buffer)+len(data) > maxBufferSize {
		flushOffset := fileStruct.Offset - int64(len(fileStruct.Buffer))
		resp, err := c.send(ps.MsgWrite, ps.WriteRequest{
			InodeID: fileStruct.InodeID,
			Offset:  flushOffset,
			Data:    fileStruct.Buffer,
		})
		if err != nil {
			return 0, fmt.Errorf("write %q failed: %w", fileStruct.FilePath, err)
		}
		if resp.Code != communication.CodeOK {
			return 0, responseError("write", fileStruct.FilePath, resp)
		}
		fileStruct.Buffer = fileStruct.Buffer[:0]
	}

	fileStruct.Buffer = append(fileStruct.Buffer, data...)
	fileStruct.Offset += int64(len(data))
	return len(data), nil
}

// Fsync flushes all buffered write data for fd to the server.
//
// Behavior:
// - If the buffer is empty, returns nil.
// - Flushes at (Offset - len(Buffer)) and clears the buffer on success.
//
// Thread-safety:
// - Acquires TableMu read lock to resolve fd.
// - Acquires SandstoreFD.Mu exclusively while checking/flushing the buffer.
//
// Consistency:
// - Uses synchronous MsgWrite; success means the write reached the server's
//   replicated write path.
func (c *SandstoreClient) Fsync(fd int) error {
	if c == nil {
		return fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return fmt.Errorf("sandstore server address is empty")
	}
	if fd < 0 {
		return fmt.Errorf("bad file descriptor")
	}

	c.TableMu.RLock()
	fileStruct := c.OpenFiles[uint64(fd)]
	c.TableMu.RUnlock()

	if fileStruct == nil {
		return fmt.Errorf("bad file descriptor")
	}

	fileStruct.Mu.Lock()
	defer fileStruct.Mu.Unlock()

	if len(fileStruct.Buffer) == 0 {
		return nil
	}

	flushOffset := fileStruct.Offset - int64(len(fileStruct.Buffer))
	resp, err := c.send(ps.MsgWrite, ps.WriteRequest{
		InodeID: fileStruct.InodeID,
		Offset:  flushOffset,
		Data:    fileStruct.Buffer,
	})
	if err != nil {
		return fmt.Errorf("fsync %q failed: %w", fileStruct.FilePath, err)
	}
	if resp.Code != communication.CodeOK {
		return responseError("fsync", fileStruct.FilePath, resp)
	}

	fileStruct.Buffer = fileStruct.Buffer[:0]
	return nil
}

// Close detaches fd from the client table and flushes any remaining buffered
// data for that descriptor.
//
// Behavior:
// - Removes fd from OpenFiles first.
// - Then waits on the file mutex and performs a final buffer flush if needed.
// - If final flush fails, returns an error even though fd is already removed.
//
// Thread-safety:
// - Acquires TableMu write lock to remove fd from OpenFiles.
// - Acquires SandstoreFD.Mu afterward to serialize against in-flight file ops.
//
// Consistency:
// - Final flush uses synchronous MsgWrite through the replicated write path.
func (c *SandstoreClient) Close(fd int) error {
	if c == nil {
		return fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return fmt.Errorf("sandstore server address is empty")
	}
	if fd < 0 {
		return fmt.Errorf("bad file descriptor")
	}

	c.TableMu.Lock()
	fileStruct := c.OpenFiles[uint64(fd)]
	if fileStruct == nil {
		c.TableMu.Unlock()
		return fmt.Errorf("bad file descriptor")
	}
	delete(c.OpenFiles, uint64(fd))
	c.TableMu.Unlock()

	fileStruct.Mu.Lock()
	defer fileStruct.Mu.Unlock()

	if len(fileStruct.Buffer) > 0 {
		flushOffset := fileStruct.Offset - int64(len(fileStruct.Buffer))
		resp, err := c.send(ps.MsgWrite, ps.WriteRequest{
			InodeID: fileStruct.InodeID,
			Offset:  flushOffset,
			Data:    fileStruct.Buffer,
		})
		if err != nil {
			return fmt.Errorf("close %q failed: %w", fileStruct.FilePath, err)
		}
		if resp.Code != communication.CodeOK {
			return responseError("close", fileStruct.FilePath, resp)
		}
		fileStruct.Buffer = fileStruct.Buffer[:0]
	}

	return nil
}

// Remove unlinks filepath on the server and eagerly removes any matching open
// descriptor entry from the local table.
//
// Behavior:
// - Holds the global table lock for the full operation.
// - If filepath is currently open, locks that file entry before RPC.
// - Resolves parent inode, sends MsgRemove, and deletes the matching local fd
//   on success.
//
// Thread-safety:
// - Acquires TableMu write lock for the full method ("big lock").
// - Optionally acquires SandstoreFD.Mu for the matched open file.
//
// Consistency:
// - Parent/target lookups are server metadata reads.
// - MsgRemove is a synchronous, Raft-replicated metadata mutation.
func (c *SandstoreClient) Remove(filepath string) error {
	if c == nil {
		return fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return fmt.Errorf("sandstore server address is empty")
	}

	cleanPath, err := normalizePath(filepath)
	if err != nil {
		return err
	}

	parentPath, name, err := splitParentAndName(cleanPath)
	if err != nil {
		return err
	}

	c.TableMu.Lock()
	defer c.TableMu.Unlock()

	var targetFD uint64
	var targetStruct *SandstoreFD
	for fd, fileStruct := range c.OpenFiles {
		if fileStruct.FilePath == cleanPath {
			targetFD = fd
			targetStruct = fileStruct
			break
		}
	}

	var inodeID string
	if targetStruct != nil {
		inodeID = targetStruct.InodeID
		targetStruct.Mu.Lock()
	} else {
		var lookupResp *communication.Response
		inodeID, lookupResp, err = c.lookupPath(cleanPath)
		if err != nil {
			return err
		}
		if lookupResp.Code != communication.CodeOK {
			return responseError("remove", cleanPath, lookupResp)
		}
	}

	if inodeID == "" {
		if targetStruct != nil {
			targetStruct.Mu.Unlock()
		}
		return fmt.Errorf("remove %q failed: missing inode id", cleanPath)
	}

	parentID, parentResp, err := c.lookupPath(parentPath)
	if err != nil {
		if targetStruct != nil {
			targetStruct.Mu.Unlock()
		}
		return err
	}
	if parentResp.Code != communication.CodeOK {
		if targetStruct != nil {
			targetStruct.Mu.Unlock()
		}
		return responseError("remove", cleanPath, parentResp)
	}

	resp, err := c.send(ps.MsgRemove, ps.RemoveRequest{
		ParentID: parentID,
		Name:     name,
	})
	if err != nil {
		if targetStruct != nil {
			targetStruct.Mu.Unlock()
		}
		return fmt.Errorf("remove %q failed: %w", cleanPath, err)
	}
	if resp.Code != communication.CodeOK {
		if targetStruct != nil {
			targetStruct.Mu.Unlock()
		}
		return responseError("remove", cleanPath, resp)
	}

	if targetStruct != nil {
		delete(c.OpenFiles, targetFD)
		targetStruct.Mu.Unlock()
	}

	return nil
}

// Rename atomically moves src to dst on the server and updates matching local
// open-file path cache entries.
//
// Behavior:
// - Resolves source and destination parent inode IDs.
// - Sends MsgRename with {SrcParentID, SrcName, DstParentID, DstName}.
// - Updates FilePath from src -> dst for local entries that currently point to
//   src only; descriptors already pointing to dst are intentionally unchanged.
//
// Thread-safety:
// - Acquires TableMu write lock for the full RPC + local update sequence
//   ("world freeze").
// - Acquires SandstoreFD.Mu while mutating each matching FilePath.
//
// Consistency:
// - MsgRename is a synchronous, Raft-replicated atomic metadata operation.
func (c *SandstoreClient) Rename(src string, dst string) error {
	if c == nil {
		return fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return fmt.Errorf("sandstore server address is empty")
	}

	cleanSrc, err := normalizePath(src)
	if err != nil {
		return err
	}
	cleanDst, err := normalizePath(dst)
	if err != nil {
		return err
	}

	srcParentPath, srcName, err := splitParentAndName(cleanSrc)
	if err != nil {
		return err
	}
	dstParentPath, dstName, err := splitParentAndName(cleanDst)
	if err != nil {
		return err
	}

	c.TableMu.Lock()
	defer c.TableMu.Unlock()

	srcParentID, srcParentResp, err := c.lookupPath(srcParentPath)
	if err != nil {
		return err
	}
	if srcParentResp.Code != communication.CodeOK {
		return responseError("rename", cleanSrc, srcParentResp)
	}

	dstParentID, dstParentResp, err := c.lookupPath(dstParentPath)
	if err != nil {
		return err
	}
	if dstParentResp.Code != communication.CodeOK {
		return responseError("rename", cleanDst, dstParentResp)
	}

	resp, err := c.send(ps.MsgRename, ps.RenameRequest{
		SrcParentID: srcParentID,
		SrcName:     srcName,
		DstParentID: dstParentID,
		DstName:     dstName,
	})
	if err != nil {
		return fmt.Errorf("rename %q -> %q failed: %w", cleanSrc, cleanDst, err)
	}
	if resp.Code != communication.CodeOK {
		return responseError("rename", cleanSrc, resp)
	}

	for _, fileStruct := range c.OpenFiles {
		if fileStruct.FilePath == cleanSrc {
			fileStruct.Mu.Lock()
			fileStruct.FilePath = cleanDst
			fileStruct.Mu.Unlock()
		}
	}

	return nil
}

// Mkdir creates a directory at path with the provided mode bits.
//
// Behavior:
// - Parses path into parent path and directory name.
// - Resolves parent inode with LookupPath.
// - Sends MsgMkdir with parent inode, name, mode, uid=0, gid=0.
//
// Thread-safety:
// - Acquires no TableMu or per-file locks; this method does not read or mutate
//   OpenFiles.
//
// Consistency:
// - Parent lookup is a server metadata read.
// - MsgMkdir is a synchronous, Raft-replicated metadata mutation.
func (c *SandstoreClient) Mkdir(path string, mode int) error {
	if c == nil {
		return fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return fmt.Errorf("sandstore server address is empty")
	}

	cleanPath, err := normalizePath(path)
	if err != nil {
		return err
	}

	parentPath := filepathpkg.Dir(cleanPath)
	name := filepathpkg.Base(cleanPath)
	if name == "" || name == "." || name == "/" {
		return fmt.Errorf("invalid path %q", cleanPath)
	}

	parentID, parentResp, err := c.lookupPath(parentPath)
	if err != nil {
		return err
	}
	if parentResp.Code != communication.CodeOK {
		return responseError("mkdir", cleanPath, parentResp)
	}

	resp, err := c.send(ps.MsgMkdir, ps.MkdirRequest{
		ParentID: parentID,
		Name:     name,
		Mode:     uint32(mode & 0o777),
		UID:      0,
		GID:      0,
	})
	if err != nil {
		return fmt.Errorf("mkdir %q failed: %w", cleanPath, err)
	}
	if resp.Code != communication.CodeOK {
		return responseError("mkdir", cleanPath, resp)
	}

	return nil
}

// Rmdir removes an empty directory at path.
//
// Behavior:
// - Parses path into parent path and directory name.
// - Resolves parent inode with LookupPath.
// - Sends MsgRmdir; server validates emptiness and existence.
//
// Thread-safety:
// - Acquires no TableMu or per-file locks.
//
// Consistency:
// - Parent lookup is a server metadata read.
// - MsgRmdir is a synchronous, Raft-replicated metadata mutation.
func (c *SandstoreClient) Rmdir(path string) error {
	if c == nil {
		return fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return fmt.Errorf("sandstore server address is empty")
	}

	cleanPath, err := normalizePath(path)
	if err != nil {
		return err
	}

	parentPath := filepathpkg.Dir(cleanPath)
	name := filepathpkg.Base(cleanPath)
	if name == "" || name == "." || name == "/" {
		return fmt.Errorf("invalid path %q", cleanPath)
	}

	parentID, parentResp, err := c.lookupPath(parentPath)
	if err != nil {
		return err
	}
	if parentResp.Code != communication.CodeOK {
		return responseError("rmdir", cleanPath, parentResp)
	}

	resp, err := c.send(ps.MsgRmdir, ps.RmdirRequest{
		ParentID: parentID,
		Name:     name,
	})
	if err != nil {
		return fmt.Errorf("rmdir %q failed: %w", cleanPath, err)
	}
	if resp.Code != communication.CodeOK {
		return responseError("rmdir", cleanPath, resp)
	}

	return nil
}

// ListDir returns all directory entries under path by paging through MsgReadDir.
//
// Behavior:
// - Resolves target inode with LookupPath.
// - Repeatedly issues ReadDir requests with cookie pagination until EOF.
// - Aggregates and returns all entries in response order.
//
// Thread-safety:
// - Acquires no TableMu or per-file locks.
//
// Consistency:
// - LookupPath is a metadata read.
// - MsgReadDir is a local server read path and therefore eventually consistent.
func (c *SandstoreClient) ListDir(path string) ([]pms.DirEntry, error) {
	if c == nil {
		return nil, fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return nil, fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return nil, fmt.Errorf("sandstore server address is empty")
	}

	cleanPath, err := normalizePath(path)
	if err != nil {
		return nil, err
	}

	inodeID, lookupResp, err := c.lookupPath(cleanPath)
	if err != nil {
		return nil, err
	}
	if lookupResp.Code != communication.CodeOK {
		return nil, responseError("listdir", cleanPath, lookupResp)
	}

	const batchSize = 100
	allEntries := make([]pms.DirEntry, 0)
	cookie := 0
	eof := false

	for !eof {
		resp, err := c.send(ps.MsgReadDir, ps.ReadDirRequest{
			InodeID:    inodeID,
			Cookie:     cookie,
			MaxEntries: batchSize,
		})
		if err != nil {
			return nil, fmt.Errorf("listdir %q failed: %w", cleanPath, err)
		}
		if resp.Code != communication.CodeOK {
			return nil, responseError("listdir", cleanPath, resp)
		}

		var page struct {
			Entries []pms.DirEntry `json:"entries"`
			Cookie  int            `json:"cookie"`
			EOF     bool           `json:"eof"`
		}
		if err := json.Unmarshal(resp.Body, &page); err != nil {
			return nil, fmt.Errorf("failed to decode listdir response for %q: %w", cleanPath, err)
		}

		allEntries = append(allEntries, page.Entries...)
		cookie = page.Cookie
		eof = page.EOF
	}

	return allEntries, nil
}

// Stat returns inode attributes for path.
//
// Behavior:
// - Resolves inode ID with LookupPath.
// - Calls MsgGetAttr and decodes pms.Attributes from the response body.
//
// Thread-safety:
// - Acquires no TableMu or per-file locks.
//
// Consistency:
// - LookupPath and MsgGetAttr are server read paths and are eventually
//   consistent with committed cluster state.
func (c *SandstoreClient) Stat(path string) (*pms.Attributes, error) {
	if c == nil {
		return nil, fmt.Errorf("sandstore client is nil")
	}
	if c.Comm == nil {
		return nil, fmt.Errorf("sandstore communicator is nil")
	}
	if c.ServerAddr == "" {
		return nil, fmt.Errorf("sandstore server address is empty")
	}

	cleanPath, err := normalizePath(path)
	if err != nil {
		return nil, err
	}

	inodeID, lookupResp, err := c.lookupPath(cleanPath)
	if err != nil {
		return nil, err
	}
	if lookupResp.Code != communication.CodeOK {
		return nil, responseError("stat", cleanPath, lookupResp)
	}

	resp, err := c.send(ps.MsgGetAttr, ps.GetAttrRequest{InodeID: inodeID})
	if err != nil {
		return nil, fmt.Errorf("stat %q failed: %w", cleanPath, err)
	}
	if resp.Code != communication.CodeOK {
		return nil, responseError("stat", cleanPath, resp)
	}

	attrs := &pms.Attributes{}
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, attrs); err != nil {
			return nil, fmt.Errorf("failed to decode stat response for %q: %w", cleanPath, err)
		}
	}

	return attrs, nil
}

func (c *SandstoreClient) addFD(inodeID string, filePath string, mode int) (int, error) {
	c.TableMu.Lock()
	defer c.TableMu.Unlock()

	if c.OpenFiles == nil {
		c.OpenFiles = make(map[uint64]*SandstoreFD)
	}

	fd := firstUserFD
	for {
		if _, exists := c.OpenFiles[fd]; !exists {
			break
		}
		if fd == math.MaxUint64 {
			return 0, fmt.Errorf("no free file descriptor available")
		}
		fd++
	}

	if fd > uint64(math.MaxInt) {
		return 0, fmt.Errorf("file descriptor exceeds int range")
	}

	c.OpenFiles[fd] = &SandstoreFD{
		FD:       fd,
		InodeID:  inodeID,
		FilePath: filePath,
		Mode:     mode,
		Offset:   0,
		Buffer:   make([]byte, 0),
	}

	return int(fd), nil
}

func (c *SandstoreClient) lookupPath(path string) (string, *communication.Response, error) {
	resp, err := c.send(ps.MsgLookupPath, ps.LookupPathRequest{Path: path})
	if err != nil {
		return "", nil, fmt.Errorf("lookup %q failed: %w", path, err)
	}

	if resp.Code != communication.CodeOK {
		return "", resp, nil
	}

	inodeID := strings.TrimSpace(string(resp.Body))
	if inodeID == "" || strings.HasPrefix(inodeID, "{") || strings.HasPrefix(inodeID, "\"") {
		var decoded string
		if err := json.Unmarshal(resp.Body, &decoded); err != nil {
			return "", nil, fmt.Errorf("failed to decode lookup response for %q: %w", path, err)
		}
		inodeID = decoded
	}

	return inodeID, resp, nil
}

func (c *SandstoreClient) createPath(path string, mode int) (*communication.Response, error) {
	parentPath, name, err := splitParentAndName(path)
	if err != nil {
		return nil, err
	}

	parentID, parentResp, err := c.lookupPath(parentPath)
	if err != nil {
		return nil, err
	}
	if parentResp.Code != communication.CodeOK {
		return parentResp, nil
	}

	createMode := uint32(mode & 0o777)
	if createMode == 0 {
		createMode = 0o644
	}

	return c.send(ps.MsgCreate, ps.CreateRequest{
		ParentID: parentID,
		Name:     name,
		Mode:     createMode,
		UID:      0,
		GID:      0,
	})
}

func (c *SandstoreClient) send(msgType string, payload any) (*communication.Response, error) {
	return c.Comm.Send(context.Background(), c.ServerAddr, communication.Message{
		From:    "sandlib",
		Type:    msgType,
		Payload: payload,
	})
}

func splitParentAndName(path string) (string, string, error) {
	if path == "/" {
		return "", "", fmt.Errorf("cannot create root path")
	}

	parent := pathpkg.Dir(path)
	if parent == "." || parent == "" {
		parent = "/"
	}

	name := pathpkg.Base(path)
	if name == "" || name == "." || name == "/" {
		return "", "", fmt.Errorf("invalid path %q", path)
	}

	return parent, name, nil
}

func normalizePath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("invalid path: empty path")
	}

	cleanPath := pathpkg.Clean(trimmed)
	if !strings.HasPrefix(cleanPath, "/") {
		return "", fmt.Errorf("invalid path %q: expected absolute path", path)
	}

	return cleanPath, nil
}

func decodeInodeID(body []byte) (string, error) {
	var inode pms.Inode
	if err := json.Unmarshal(body, &inode); err != nil {
		return "", fmt.Errorf("failed to decode create response: %w", err)
	}
	if inode.InodeID == "" {
		return "", fmt.Errorf("create response missing inode id")
	}
	return inode.InodeID, nil
}

func responseError(op string, path string, resp *communication.Response) error {
	if resp == nil {
		return fmt.Errorf("%s %q failed: empty response", op, path)
	}

	body := strings.TrimSpace(string(resp.Body))
	if body == "" {
		body = string(resp.Code)
	}

	switch resp.Code {
	case communication.CodeNotFound:
		return fmt.Errorf("%s %q: %w", op, path, os.ErrNotExist)
	case communication.CodeAlreadyExists:
		return fmt.Errorf("%s %q: file exists", op, path)
	default:
		return fmt.Errorf("%s %q failed (%s): %s", op, path, resp.Code, body)
	}
}
