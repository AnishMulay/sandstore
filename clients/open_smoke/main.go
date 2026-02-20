package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/AnishMulay/sandstore/clients/library"
	grpccomm "github.com/AnishMulay/sandstore/internal/communication/grpc"
	logservice "github.com/AnishMulay/sandstore/internal/log_service"
	locallog "github.com/AnishMulay/sandstore/internal/log_service/localdisc"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
)

const maxBufferSize = 2 * 1024 * 1024

func main() {
	serverAddr := os.Getenv("SANDSTORE_ADDR")
	if serverAddr == "" {
		serverAddr = "127.0.0.1:9001"
	}

	logDir := filepath.Join("run", "smoke", "logs")
	ls := locallog.NewLocalDiscLogService(logDir, "smoke", logservice.InfoLevel)
	comm := grpccomm.NewGRPCCommunicator(":0", ls)
	client := sandlib.NewSandstoreClient(serverAddr, comm)

	path := fmt.Sprintf("/sandlib-smoke-open-read-write-%d.txt", time.Now().UnixNano())

	fdCreate, err := client.Open(path, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(create) failed on %s for %s: %v", serverAddr, path, err)
	}
	log.Printf("PASS: Open(create) returned fd=%d for %s", fdCreate, path)

	dataCreate, err := readFromFreshFD(client, path, 64)
	if err != nil {
		log.Fatalf("Read(initial) failed on %s for %s: %v", serverAddr, path, err)
	}
	if len(dataCreate) != 0 {
		log.Fatalf("Read(initial) expected empty data, got %d bytes", len(dataCreate))
	}
	log.Printf("PASS: Read(initial) returned %d bytes for %s", len(dataCreate), path)

	fdLookup, err := client.Open(path, os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(lookup) failed on %s for %s: %v", serverAddr, path, err)
	}
	log.Printf("PASS: Open(lookup) returned fd=%d for %s", fdLookup, path)

	dataLookup, err := client.Read(fdLookup, 64)
	if err != nil {
		log.Fatalf("Read(lookup-fd) failed on %s for %s fd=%d: %v", serverAddr, path, fdLookup, err)
	}
	if len(dataLookup) != 0 {
		log.Fatalf("Read(lookup-fd) expected empty data, got %d bytes", len(dataLookup))
	}
	log.Printf("PASS: Read(lookup-fd) returned %d bytes for %s", len(dataLookup), path)

	chunkA := makePatternChunk(1*1024*1024, "chunk-a")
	chunkB := makePatternChunk(1536*1024, "chunk-b")
	chunkC := makePatternChunk(1536*1024, "chunk-c")

	writtenA, err := client.Write(fdCreate, chunkA)
	if err != nil {
		log.Fatalf("Write(chunkA) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	if writtenA != len(chunkA) {
		log.Fatalf("Write(chunkA) expected %d bytes, got %d", len(chunkA), writtenA)
	}
	log.Printf("PASS: Write(chunkA) wrote %d bytes", writtenA)

	dataAfterA, err := readFromFreshFD(client, path, len(chunkA)+128)
	if err != nil {
		log.Fatalf("Read(after chunkA) failed on %s for %s: %v", serverAddr, path, err)
	}
	if len(dataAfterA) != 0 {
		log.Fatalf("Read(after chunkA) expected 0 persisted bytes, got %d", len(dataAfterA))
	}
	log.Printf("PASS: Buffered Write(chunkA) not visible before flush")

	writtenB, err := client.Write(fdCreate, chunkB)
	if err != nil {
		log.Fatalf("Write(chunkB) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	if writtenB != len(chunkB) {
		log.Fatalf("Write(chunkB) expected %d bytes, got %d", len(chunkB), writtenB)
	}
	log.Printf("PASS: Write(chunkB) wrote %d bytes (triggered flush of chunkA)", writtenB)

	dataAfterB, err := readFromFreshFD(client, path, len(chunkA)+len(chunkB)+128)
	if err != nil {
		log.Fatalf("Read(after chunkB) failed on %s for %s: %v", serverAddr, path, err)
	}
	if !bytes.Equal(dataAfterB, chunkA) {
		log.Fatalf("Read(after chunkB) expected exactly chunkA (%d bytes), got %d bytes", len(chunkA), len(dataAfterB))
	}
	log.Printf("PASS: Flush-on-overflow persisted chunkA with correct ordering/offset")

	writtenC, err := client.Write(fdCreate, chunkC)
	if err != nil {
		log.Fatalf("Write(chunkC) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	if writtenC != len(chunkC) {
		log.Fatalf("Write(chunkC) expected %d bytes, got %d", len(chunkC), writtenC)
	}
	log.Printf("PASS: Write(chunkC) wrote %d bytes (triggered flush of chunkB)", writtenC)

	expectedAfterC := append(append(make([]byte, 0, len(chunkA)+len(chunkB)), chunkA...), chunkB...)
	dataAfterC, err := readFromFreshFD(client, path, len(expectedAfterC)+len(chunkC)+128)
	if err != nil {
		log.Fatalf("Read(after chunkC) failed on %s for %s: %v", serverAddr, path, err)
	}
	if !bytes.Equal(dataAfterC, expectedAfterC) {
		log.Fatalf("Read(after chunkC) expected chunkA+chunkB (%d bytes), got %d bytes", len(expectedAfterC), len(dataAfterC))
	}
	log.Printf("PASS: Sequential flushes persisted chunkA+chunkB in exact order")

	if err := client.Fsync(fdCreate); err != nil {
		log.Fatalf("Fsync(chunkC flush) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	log.Printf("PASS: Fsync(chunkC flush) returned success")

	expectedAfterFsync := append(append(make([]byte, 0, len(expectedAfterC)+len(chunkC)), expectedAfterC...), chunkC...)
	dataAfterFsync, err := readFromFreshFD(client, path, len(expectedAfterFsync)+128)
	if err != nil {
		log.Fatalf("Read(after fsync) failed on %s for %s: %v", serverAddr, path, err)
	}
	if !bytes.Equal(dataAfterFsync, expectedAfterFsync) {
		log.Fatalf("Read(after fsync) expected chunkA+chunkB+chunkC (%d bytes), got %d bytes", len(expectedAfterFsync), len(dataAfterFsync))
	}
	log.Printf("PASS: Fsync persisted buffered chunkC at correct offset")

	if err := client.Fsync(fdCreate); err != nil {
		log.Fatalf("Fsync(empty buffer) failed on %s for %s fd=%d: %v", serverAddr, path, fdCreate, err)
	}
	log.Printf("PASS: Fsync(empty buffer) returned success")

	closePath := fmt.Sprintf("/sandlib-smoke-close-%d.txt", time.Now().UnixNano())
	fdClose, err := client.Open(closePath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(close target) failed on %s for %s: %v", serverAddr, closePath, err)
	}
	log.Printf("PASS: Open(close target) returned fd=%d for %s", fdClose, closePath)

	closeChunk := makePatternChunk(768*1024, "close-chunk")
	writtenClose, err := client.Write(fdClose, closeChunk)
	if err != nil {
		log.Fatalf("Write(close chunk) failed on %s for %s fd=%d: %v", serverAddr, closePath, fdClose, err)
	}
	if writtenClose != len(closeChunk) {
		log.Fatalf("Write(close chunk) expected %d bytes, got %d", len(closeChunk), writtenClose)
	}
	log.Printf("PASS: Write(close chunk) wrote %d buffered bytes", writtenClose)

	dataBeforeClose, err := readFromFreshFD(client, closePath, len(closeChunk)+128)
	if err != nil {
		log.Fatalf("Read(before close flush) failed on %s for %s: %v", serverAddr, closePath, err)
	}
	if len(dataBeforeClose) != 0 {
		log.Fatalf("Read(before close flush) expected 0 persisted bytes, got %d", len(dataBeforeClose))
	}
	log.Printf("PASS: Buffered Write(close chunk) not visible before Close flush")

	if err := client.Close(fdClose); err != nil {
		log.Fatalf("Close(flush buffered data) failed on %s for %s fd=%d: %v", serverAddr, closePath, fdClose, err)
	}
	log.Printf("PASS: Close(flush buffered data) returned success")

	dataAfterClose, err := readFromFreshFD(client, closePath, len(closeChunk)+128)
	if err != nil {
		log.Fatalf("Read(after close flush) failed on %s for %s: %v", serverAddr, closePath, err)
	}
	if !bytes.Equal(dataAfterClose, closeChunk) {
		log.Fatalf("Read(after close flush) expected close chunk (%d bytes), got %d bytes", len(closeChunk), len(dataAfterClose))
	}
	log.Printf("PASS: Close persisted buffered data at correct offset")

	if _, err := client.Write(fdClose, []byte("x")); err == nil {
		log.Fatalf("Write(closed fd) expected failure for fd=%d", fdClose)
	}
	if err := client.Fsync(fdClose); err == nil {
		log.Fatalf("Fsync(closed fd) expected failure for fd=%d", fdClose)
	}
	if err := client.Close(fdClose); err == nil {
		log.Fatalf("Close(closed fd) expected failure for fd=%d", fdClose)
	}
	log.Printf("PASS: Closed FD rejects Write/Fsync/Close as expected")

	emptyClosePath := fmt.Sprintf("/sandlib-smoke-close-empty-%d.txt", time.Now().UnixNano())
	fdCloseEmpty, err := client.Open(emptyClosePath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(close empty target) failed on %s for %s: %v", serverAddr, emptyClosePath, err)
	}
	if err := client.Close(fdCloseEmpty); err != nil {
		log.Fatalf("Close(empty buffer) failed on %s for %s fd=%d: %v", serverAddr, emptyClosePath, fdCloseEmpty, err)
	}
	log.Printf("PASS: Close(empty buffer) returned success")

	removePath := fmt.Sprintf("/sandlib-smoke-remove-%d.txt", time.Now().UnixNano())
	fdRemove, err := client.Open(removePath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(remove target) failed on %s for %s: %v", serverAddr, removePath, err)
	}
	if err := client.Close(fdRemove); err != nil {
		log.Fatalf("Close(remove target setup) failed on %s for %s fd=%d: %v", serverAddr, removePath, fdRemove, err)
	}
	if err := client.Remove(removePath); err != nil {
		log.Fatalf("Remove(closed file) failed on %s for %s: %v", serverAddr, removePath, err)
	}
	if _, err := client.Open(removePath, os.O_RDWR); err == nil {
		log.Fatalf("Open(removed file) expected failure for %s", removePath)
	}
	log.Printf("PASS: Remove(closed file) deleted file and made path unreachable")

	removeOpenPath := fmt.Sprintf("/sandlib-smoke-remove-open-%d.txt", time.Now().UnixNano())
	fdRemoveOpen, err := client.Open(removeOpenPath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(remove-open target) failed on %s for %s: %v", serverAddr, removeOpenPath, err)
	}
	if _, err := client.Write(fdRemoveOpen, []byte("remove-open-buffered")); err != nil {
		log.Fatalf("Write(remove-open target) failed on %s for %s fd=%d: %v", serverAddr, removeOpenPath, fdRemoveOpen, err)
	}
	if err := client.Remove(removeOpenPath); err != nil {
		log.Fatalf("Remove(open file) failed on %s for %s: %v", serverAddr, removeOpenPath, err)
	}
	if _, err := client.Write(fdRemoveOpen, []byte("x")); err == nil {
		log.Fatalf("Write(removed open fd) expected failure for fd=%d", fdRemoveOpen)
	}
	if _, err := client.Open(removeOpenPath, os.O_RDWR); err == nil {
		log.Fatalf("Open(removed open file) expected failure for %s", removeOpenPath)
	}
	log.Printf("PASS: Remove(open file) performed eager close and removed path")

	missingRemovePath := fmt.Sprintf("/sandlib-smoke-remove-missing-%d.txt", time.Now().UnixNano())
	if err := client.Remove(missingRemovePath); err == nil {
		log.Fatalf("Remove(missing file) expected failure for %s", missingRemovePath)
	}
	log.Printf("PASS: Remove(missing file) returned expected error")

	renameSrcPath := fmt.Sprintf("/sandlib-smoke-rename-src-%d.txt", time.Now().UnixNano())
	renameDstPath := fmt.Sprintf("/sandlib-smoke-rename-dst-%d.txt", time.Now().UnixNano())
	fdRenameClosed, err := client.Open(renameSrcPath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(rename closed src) failed on %s for %s: %v", serverAddr, renameSrcPath, err)
	}
	renameClosedData := []byte("rename-closed-file-data")
	if _, err := client.Write(fdRenameClosed, renameClosedData); err != nil {
		log.Fatalf("Write(rename closed src) failed on %s for %s fd=%d: %v", serverAddr, renameSrcPath, fdRenameClosed, err)
	}
	if err := client.Fsync(fdRenameClosed); err != nil {
		log.Fatalf("Fsync(rename closed src) failed on %s for %s fd=%d: %v", serverAddr, renameSrcPath, fdRenameClosed, err)
	}
	if err := client.Close(fdRenameClosed); err != nil {
		log.Fatalf("Close(rename closed src) failed on %s for %s fd=%d: %v", serverAddr, renameSrcPath, fdRenameClosed, err)
	}
	if err := client.Rename(renameSrcPath, renameDstPath); err != nil {
		log.Fatalf("Rename(closed file) failed on %s for %s -> %s: %v", serverAddr, renameSrcPath, renameDstPath, err)
	}
	if _, err := client.Open(renameSrcPath, os.O_RDWR); err == nil {
		log.Fatalf("Open(renamed old src) expected failure for %s", renameSrcPath)
	}
	renameClosedRead, err := readFromFreshFD(client, renameDstPath, len(renameClosedData)+64)
	if err != nil {
		log.Fatalf("Read(rename closed dst) failed on %s for %s: %v", serverAddr, renameDstPath, err)
	}
	if !bytes.Equal(renameClosedRead, renameClosedData) {
		log.Fatalf("Read(rename closed dst) expected %d bytes, got %d bytes", len(renameClosedData), len(renameClosedRead))
	}
	log.Printf("PASS: Rename(closed file) moved path and preserved data")

	renameOpenSrcPath := fmt.Sprintf("/sandlib-smoke-rename-open-src-%d.txt", time.Now().UnixNano())
	renameOpenDstPath := fmt.Sprintf("/sandlib-smoke-rename-open-dst-%d.txt", time.Now().UnixNano())
	fdRenameOpen, err := client.Open(renameOpenSrcPath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(rename open src) failed on %s for %s: %v", serverAddr, renameOpenSrcPath, err)
	}
	renameOpenData := []byte("rename-open-file-buffered-data")
	if _, err := client.Write(fdRenameOpen, renameOpenData); err != nil {
		log.Fatalf("Write(rename open src) failed on %s for %s fd=%d: %v", serverAddr, renameOpenSrcPath, fdRenameOpen, err)
	}
	if err := client.Rename(renameOpenSrcPath, renameOpenDstPath); err != nil {
		log.Fatalf("Rename(open file) failed on %s for %s -> %s: %v", serverAddr, renameOpenSrcPath, renameOpenDstPath, err)
	}
	if err := client.Fsync(fdRenameOpen); err != nil {
		log.Fatalf("Fsync(rename open fd after rename) failed on %s for %s fd=%d: %v", serverAddr, renameOpenDstPath, fdRenameOpen, err)
	}
	if _, err := client.Open(renameOpenSrcPath, os.O_RDWR); err == nil {
		log.Fatalf("Open(renamed open old src) expected failure for %s", renameOpenSrcPath)
	}
	renameOpenRead, err := readFromFreshFD(client, renameOpenDstPath, len(renameOpenData)+64)
	if err != nil {
		log.Fatalf("Read(rename open dst) failed on %s for %s: %v", serverAddr, renameOpenDstPath, err)
	}
	if !bytes.Equal(renameOpenRead, renameOpenData) {
		log.Fatalf("Read(rename open dst) expected %d bytes, got %d bytes", len(renameOpenData), len(renameOpenRead))
	}
	if err := client.Remove(renameOpenDstPath); err != nil {
		log.Fatalf("Remove(rename open dst) failed on %s for %s: %v", serverAddr, renameOpenDstPath, err)
	}
	if _, err := client.Write(fdRenameOpen, []byte("x")); err == nil {
		log.Fatalf("Write(renamed open removed fd) expected failure for fd=%d", fdRenameOpen)
	}
	if err := client.Close(fdRenameOpen); err == nil {
		log.Fatalf("Close(renamed open removed fd) expected failure for fd=%d", fdRenameOpen)
	}
	log.Printf("PASS: Rename(open file) updated live FD path and eager close behaved correctly")

	missingRenameSrcPath := fmt.Sprintf("/sandlib-smoke-rename-missing-src-%d.txt", time.Now().UnixNano())
	missingRenameDstPath := fmt.Sprintf("/sandlib-smoke-rename-missing-dst-%d.txt", time.Now().UnixNano())
	if err := client.Rename(missingRenameSrcPath, missingRenameDstPath); err == nil {
		log.Fatalf("Rename(missing src) expected failure for %s -> %s", missingRenameSrcPath, missingRenameDstPath)
	}
	log.Printf("PASS: Rename(missing src) returned expected error")

	mkdirPath := fmt.Sprintf("/sandlib-smoke-mkdir-%d", time.Now().UnixNano())
	if err := client.Mkdir(mkdirPath, 0o755); err != nil {
		log.Fatalf("Mkdir(valid path) failed on %s for %s: %v", serverAddr, mkdirPath, err)
	}
	log.Printf("PASS: Mkdir(valid path) created directory %s", mkdirPath)

	mkdirFilePath := fmt.Sprintf("%s/%s", mkdirPath, "mkdir-probe.txt")
	fdMkdirProbe, err := client.Open(mkdirFilePath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(file in mkdir path) failed on %s for %s: %v", serverAddr, mkdirFilePath, err)
	}
	if err := client.Close(fdMkdirProbe); err != nil {
		log.Fatalf("Close(file in mkdir path) failed on %s for %s fd=%d: %v", serverAddr, mkdirFilePath, fdMkdirProbe, err)
	}
	if err := client.Remove(mkdirFilePath); err != nil {
		log.Fatalf("Remove(file in mkdir path) failed on %s for %s: %v", serverAddr, mkdirFilePath, err)
	}
	log.Printf("PASS: Mkdir(valid path) is usable for file create/open/remove")

	if err := client.Mkdir(mkdirPath, 0o755); err == nil {
		log.Fatalf("Mkdir(existing path) expected failure for %s", mkdirPath)
	}
	log.Printf("PASS: Mkdir(existing path) returned expected error")

	missingParentMkdirPath := fmt.Sprintf("/sandlib-smoke-mkdir-missing-parent-%d/child", time.Now().UnixNano())
	if err := client.Mkdir(missingParentMkdirPath, 0o755); err == nil {
		log.Fatalf("Mkdir(missing parent) expected failure for %s", missingParentMkdirPath)
	}
	log.Printf("PASS: Mkdir(missing parent) returned expected error")

	rmdirEmptyPath := fmt.Sprintf("/sandlib-smoke-rmdir-empty-%d", time.Now().UnixNano())
	if err := client.Mkdir(rmdirEmptyPath, 0o755); err != nil {
		log.Fatalf("Mkdir(rmdir empty target) failed on %s for %s: %v", serverAddr, rmdirEmptyPath, err)
	}
	if err := client.Rmdir(rmdirEmptyPath); err != nil {
		log.Fatalf("Rmdir(empty dir) failed on %s for %s: %v", serverAddr, rmdirEmptyPath, err)
	}
	if err := client.Mkdir(fmt.Sprintf("%s/%s", rmdirEmptyPath, "child"), 0o755); err == nil {
		log.Fatalf("Mkdir(under removed dir) expected failure for %s", rmdirEmptyPath)
	}
	log.Printf("PASS: Rmdir(empty dir) removed directory and path became unreachable")

	rmdirNonEmptyPath := fmt.Sprintf("/sandlib-smoke-rmdir-nonempty-%d", time.Now().UnixNano())
	if err := client.Mkdir(rmdirNonEmptyPath, 0o755); err != nil {
		log.Fatalf("Mkdir(rmdir non-empty target) failed on %s for %s: %v", serverAddr, rmdirNonEmptyPath, err)
	}
	rmdirProbeFilePath := fmt.Sprintf("%s/%s", rmdirNonEmptyPath, "probe.txt")
	fdRmdirProbe, err := client.Open(rmdirProbeFilePath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(rmdir non-empty probe) failed on %s for %s: %v", serverAddr, rmdirProbeFilePath, err)
	}
	if err := client.Close(fdRmdirProbe); err != nil {
		log.Fatalf("Close(rmdir non-empty probe) failed on %s for %s fd=%d: %v", serverAddr, rmdirProbeFilePath, fdRmdirProbe, err)
	}
	if err := client.Rmdir(rmdirNonEmptyPath); err == nil {
		log.Fatalf("Rmdir(non-empty dir) expected failure for %s", rmdirNonEmptyPath)
	}
	if err := client.Remove(rmdirProbeFilePath); err != nil {
		log.Fatalf("Remove(rmdir non-empty probe) failed on %s for %s: %v", serverAddr, rmdirProbeFilePath, err)
	}
	if err := client.Rmdir(rmdirNonEmptyPath); err != nil {
		log.Fatalf("Rmdir(after cleanup) failed on %s for %s: %v", serverAddr, rmdirNonEmptyPath, err)
	}
	log.Printf("PASS: Rmdir(non-empty dir) rejected until empty, then succeeded")

	missingRmdirPath := fmt.Sprintf("/sandlib-smoke-rmdir-missing-%d", time.Now().UnixNano())
	if err := client.Rmdir(missingRmdirPath); err == nil {
		log.Fatalf("Rmdir(missing dir) expected failure for %s", missingRmdirPath)
	}
	log.Printf("PASS: Rmdir(missing dir) returned expected error")

	listDirPath := fmt.Sprintf("/sandlib-smoke-listdir-%d", time.Now().UnixNano())
	if err := client.Mkdir(listDirPath, 0o755); err != nil {
		log.Fatalf("Mkdir(listdir root) failed on %s for %s: %v", serverAddr, listDirPath, err)
	}
	listDirChildDir := fmt.Sprintf("%s/%s", listDirPath, "child-dir")
	if err := client.Mkdir(listDirChildDir, 0o755); err != nil {
		log.Fatalf("Mkdir(listdir child dir) failed on %s for %s: %v", serverAddr, listDirChildDir, err)
	}
	listDirFileA := fmt.Sprintf("%s/%s", listDirPath, "alpha.txt")
	listDirFileB := fmt.Sprintf("%s/%s", listDirPath, "beta.txt")
	fdListDirA, err := client.Open(listDirFileA, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(listdir alpha) failed on %s for %s: %v", serverAddr, listDirFileA, err)
	}
	if err := client.Close(fdListDirA); err != nil {
		log.Fatalf("Close(listdir alpha) failed on %s for %s fd=%d: %v", serverAddr, listDirFileA, fdListDirA, err)
	}
	fdListDirB, err := client.Open(listDirFileB, os.O_CREATE|os.O_RDWR)
	if err != nil {
		log.Fatalf("Open(listdir beta) failed on %s for %s: %v", serverAddr, listDirFileB, err)
	}
	if err := client.Close(fdListDirB); err != nil {
		log.Fatalf("Close(listdir beta) failed on %s for %s fd=%d: %v", serverAddr, listDirFileB, fdListDirB, err)
	}

	entries, err := client.ListDir(listDirPath)
	if err != nil {
		log.Fatalf("ListDir(valid path) failed on %s for %s: %v", serverAddr, listDirPath, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	sort.Strings(names)
	expectedNames := []string{"alpha.txt", "beta.txt", "child-dir"}
	for _, expected := range expectedNames {
		found := false
		for _, name := range names {
			if name == expected {
				found = true
				break
			}
		}
		if !found {
			log.Fatalf("ListDir(valid path) missing entry %q in %v", expected, names)
		}
	}
	log.Printf("PASS: ListDir(valid path) returned expected entries: %v", names)

	if _, err := client.ListDir(fmt.Sprintf("%s/%s", listDirPath, "missing-dir")); err == nil {
		log.Fatalf("ListDir(missing path) expected failure under %s", listDirPath)
	}
	log.Printf("PASS: ListDir(missing path) returned expected error")

	fileAttrs, err := client.Stat(listDirFileA)
	if err != nil {
		log.Fatalf("Stat(file) failed on %s for %s: %v", serverAddr, listDirFileA, err)
	}
	if fileAttrs.InodeID == "" {
		log.Fatalf("Stat(file) expected inode id for %s", listDirFileA)
	}
	if fileAttrs.Type != pms.TypeFile {
		log.Fatalf("Stat(file) expected file type for %s, got %d", listDirFileA, fileAttrs.Type)
	}
	if fileAttrs.Size != 0 {
		log.Fatalf("Stat(file) expected size 0 for %s, got %d", listDirFileA, fileAttrs.Size)
	}
	log.Printf("PASS: Stat(file) returned expected attributes for %s", listDirFileA)

	dirAttrs, err := client.Stat(listDirPath)
	if err != nil {
		log.Fatalf("Stat(dir) failed on %s for %s: %v", serverAddr, listDirPath, err)
	}
	if dirAttrs.InodeID == "" {
		log.Fatalf("Stat(dir) expected inode id for %s", listDirPath)
	}
	if dirAttrs.Type != pms.TypeDirectory {
		log.Fatalf("Stat(dir) expected directory type for %s, got %d", listDirPath, dirAttrs.Type)
	}
	log.Printf("PASS: Stat(dir) returned expected attributes for %s", listDirPath)

	if _, err := client.Stat(fmt.Sprintf("%s/%s", listDirPath, "missing-stat.txt")); err == nil {
		log.Fatalf("Stat(missing path) expected failure under %s", listDirPath)
	}
	log.Printf("PASS: Stat(missing path) returned expected error")

	if err := client.Remove(listDirFileA); err != nil {
		log.Fatalf("Remove(listdir alpha) failed on %s for %s: %v", serverAddr, listDirFileA, err)
	}
	if err := client.Remove(listDirFileB); err != nil {
		log.Fatalf("Remove(listdir beta) failed on %s for %s: %v", serverAddr, listDirFileB, err)
	}
	if err := client.Rmdir(listDirChildDir); err != nil {
		log.Fatalf("Rmdir(listdir child dir) failed on %s for %s: %v", serverAddr, listDirChildDir, err)
	}
	if err := client.Rmdir(listDirPath); err != nil {
		log.Fatalf("Rmdir(listdir root) failed on %s for %s: %v", serverAddr, listDirPath, err)
	}
	if _, err := client.Stat(listDirFileA); err == nil {
		log.Fatalf("Stat(removed file) expected failure for %s", listDirFileA)
	}
	log.Printf("PASS: ListDir test artifacts cleaned up")

	if len(chunkA)+len(chunkB) <= maxBufferSize {
		log.Fatalf("internal smoke test invariant failed: chunkA+chunkB must exceed maxBufferSize")
	}
	if len(chunkB)+len(chunkC) <= maxBufferSize {
		log.Fatalf("internal smoke test invariant failed: chunkB+chunkC must exceed maxBufferSize")
	}

	racePath := fmt.Sprintf("/sandlib-smoke-race-%d.txt", time.Now().UnixNano())
	const workers = 8

	var wg sync.WaitGroup
	errCh := make(chan error, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			fd, openErr := client.Open(racePath, os.O_CREATE|os.O_RDWR)
			if openErr != nil {
				errCh <- fmt.Errorf("worker %d: %w", worker, openErr)
				return
			}

			data, readErr := client.Read(fd, 64)
			if readErr != nil {
				errCh <- fmt.Errorf("worker %d: read failed fd=%d: %w", worker, fd, readErr)
				return
			}
			if len(data) != 0 {
				errCh <- fmt.Errorf("worker %d: expected empty read, got %d bytes", worker, len(data))
				return
			}

			log.Printf("PASS: worker=%d Open+Read(race) fd=%d path=%s bytes=%d", worker, fd, racePath, len(data))
		}(i)
	}

	wg.Wait()
	close(errCh)

	for openErr := range errCh {
		log.Fatalf("Open(race) failed: %v", openErr)
	}
	log.Printf("PASS: Open+Read race handling validated on %s with %d workers", racePath, workers)

	log.Printf("Smoke test complete. target=%s", serverAddr)
}

func readFromFreshFD(client *sandlib.SandstoreClient, path string, n int) ([]byte, error) {
	fd, err := client.Open(path, os.O_RDWR)
	if err != nil {
		return nil, fmt.Errorf("open for read failed: %w", err)
	}

	data, err := client.Read(fd, n)
	if err != nil {
		return nil, fmt.Errorf("read failed for fd=%d: %w", fd, err)
	}

	if err := client.Close(fd); err != nil {
		return nil, fmt.Errorf("close failed for fd=%d: %w", fd, err)
	}
	return data, nil
}

func makePatternChunk(size int, token string) []byte {
	chunk := make([]byte, size)
	pattern := []byte(token)
	for i := 0; i < size; i++ {
		chunk[i] = pattern[i%len(pattern)]
	}
	return chunk
}
