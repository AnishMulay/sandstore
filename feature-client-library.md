# Feature Client Library

## Process

- [x]  Goals → list out the goals of the project.
- [x]  User stories → user centric design, 10 to 20 user stories for the project.
- [x]  Data models → define the data models and how the data models are going to be interacting with each other.
- [x]  Nail an MVP → what are the user stories which are required for a minimum viable product, get rid of the rest for now.
- [ ]  Draw the prototype → paper is cheap, code is expensive.
- [x]  What does the future of the project look like ?
- [x]  Drill into the specific components.
- [x]  Pick a stack → programming language, frameworks etc.
- [ ]  Actual software development.

---

## Goals

**Vision and Purpose**

- Provide a reusable client library that lets applications interact with a sandstore cluster using familiar posix-like file operations.
- Remove the need for developers to write custom sandstore clients for integration testing or production systems.
- Offer a consistent and standard interface that improves developer experience and eliminates duplicated client-side logic.

**Functional Goals**

- The client library must provide a posix like interface which application developers can use to interact with a sandstore server or cluster.
- The client library must be synchronous, meaning it should only return a success to the client once it receives a success from the sandstore server.
- The client library must cache file data which was recently interacted with.
- The client library must provide a way of configuring which sandstore cluster is being interacted with

**Non-Functional Goals**

- The client library must guarantee the persistence of operations once a success is returned to the calling user.
- The client library should be easy to extend, it should be easy to add new posix compliant calls later on.

---

## User Stories

**Some Notes before the actual user stories**

- Okay, the first thing I realized is that every future function will have a lot of flags associated with it. In the later sections, what I also need to do is make sure that I have the correct set of flags available as well. And what functions actually need flags?
- The other kind of doubt I have is that this will definitely be a library which has file-level access. It directly interacts with files, so it's good for an SDK or to write your own clients and what not for testing and stuff like that. If I plan to write a fuse in the future, then if I understand it correctly, there will be a separate server which exposes block-level access in some sense to the operating system, I guess. Then the fuse is what lets Linux, let's say, interact with the underlying storage without actually knowing that it's going over the network. I think that's the general idea.
- One really interesting thing that the AI pointed out was that if I have a partitioned system of sorts, because currently in Sandstore I do not have partitioning of any sort. All servers have all data in the specific configuration that I've created right now, so there is no routing layer and stuff like that. It would be a very nice LinkedIn post or article to talk about how atomic renaming works in sharded systems, sharded distributed file systems.

**User Stories**

- As a library user, when I call library.open() with a filepath, I want to get back a file descriptor, so that I can use it to perform other file operations without having to deal with the permissions and filepath every time.
- As a library user, when I call library.read(fd, n), I want the library to read the next n bytes from the file at the current offset and return them to me, so that I can use them in my application.
- As a library user, when I call library.write(fd, bytes), I want the library to write these bytes to the current fole offset, so that I can persist data I want to persist to a file.
- As a library user, when I call library.close(fd), I want the library to close this file descriptor for myself, so that I can safely assume the file is closed and move on in my application code.
- As a library user, when I call library.lseek(fd, pos), I want the library to set the current file offset based on the input parameters, so that I can access random parts of the file whenever I want to.
- As a library user, when I call library.fsync(fd) all the buffered changes should be flushed to the disc (server), so that I can guarantee that the writes are persisted before moving on in my application code.

- As a library user, when I call library.remove(filepath), I want the file to be deleted, so that I can be sure that the file no longer exists in the system.
- As a library user, when I call library.rename(src, dst), I want the renaming to be done atomically, so that I can use the rename call for my applications important atomic operations (for example).

- As a library user, when I call library.mkdir(path, mode), I want the directory to be created, so that I can start using the directory for my application code.
- As a library user, when I call library.rmdir(path), I want this directory to be deleted only if it is empty, so that I can safely assume it is deleted and move on in my application code.
- As a library user, when I call library.listdir(path) I want a list of entries in that directory to be returned, so that I can use this list for further operations in my application.

- As a library user, when I call library.stat(path), I want to get the current status of the file with all the attributes necessary, so that I can make decisions in my application code based on this.

**Failure modes and error handling**

- No automatic retires, though it will be the client's responsibility to retry failed operations, this design decision is taken to implement at most once semantics. I am trying to follow in the steps of other distributed systems.
- 30-second configurable, like a default 30 seconds and a configurable timeout for each operation which is done by the user using the library.
- Atomic operations retain their atomicity. Rename seems to be the only client-facing atomic operation. The other operations are also atomic, but they are atomic from a server point of view. These guarantees will be persisted in terms of the client.

---

## Data Models

- **sandstoreFD** - this struct represents an open file, it is like a combination of an entry in the open file table and an entry in the file descriptor table. For the client, when a file descriptor is returned to the application, the application can use the file descriptor, which will be an integer, an unsigned 64-bit integer, to index into the Sandstore file descriptor table. Instead of containing a pointer to an entry in the open file table, the Sandstore file descriptor table will contain a direct struct, and the direct struct will represent an open file in the context of that library.
    
    The components of the struct will be
    
    - The file descriptor which we can call fd
    - An inode ID which will be used to uniquely identify a file in the Sandstore server.
    - The actual file path; this will be more like a cache, because the actual file path resolution will be done on the open call by a SandStore server.
    - Mod. This mod is just a placeholder for now because permissions have not been implemented in Sandstorm, but it will be useful in the future.
    - Offset the offset into the file, basically the current cursor position in the file.
    - A write buffer which buffers the write until fsync or the buffer limit is exceeded
    - And a mutex or lock associated with this particular struct, so that if multiple goroutines are accessing the same file in the same Go process, they can do so in a deterministic manner.
- ClientManager - this basically represents the entire state of the land, which can be described by a few things:
    1. The main thing is a map of file descriptors to structs.
    2. Even though it's a map, it's essentially a file descriptor table, the entire file descriptor table.
    3. Instead of the file descriptors pointing to an entry in the file open file table (in the case of operating systems), this just stores the entire struct, which uniquely identifies a particular file that the client, which is using this library, is working with.
    
    There is a function to get the next file descriptor, next free file descriptor. It would be interesting to know how this is actually done in an operating system and how that can map to a distributed system client like this.
    And finally, there will be a read-write lock on multiple readers, single writer lock on the entire file descriptor table itself, so that when an entry is removed or added to the entire table, the table itself is protected from concurrent access. 
    

---

## MVP

- Well, I have purposefully chosen the most minimal set that I can implement from a client's perspective because I want to cover breadth first before moving on to depth. No further removals can be done; it is already in MVP.

---

## Prototype

- I will be inserting a architecture diagram specifying the general nature of the flows here.

---

## Project future

- Fine grained locking and concurrency optimization.
- Sandstore test suite for load testing and testing out different scenarios from literature.
- Sharded file storage.
- FUSE interface.
- Benchmarking and profiling scripts on top of the client library **sandlib**.

---

## Specific Components

### 1. library.Open(fd, mode)

**Inputs:**

- `filepath` (string): Absolute path to the file.
- `mode` (int): Bitmask for permissions/flags (e.g., ReadOnly, Create).

**Logic Flow:**

```go
function Open(filepath, mode) returns (int, error):

	// 1. Validate Input
	if filepath is empty: return Error("Invalid Path")
	
	// 2. Server Interaction (OUTSIDE the lock to avoid blocking other clients)
	// We need to resolve the path to an Inode ID.
	// Note: If 'mode' includes CREATE flag, we might call Server.Create() here instead.
	inode_id, err := server.Lookup(filepath)
	
	if err == NotFound:
	    return Error("File not found") // Or handle creation if flag is set
	if err != nil:
	    return Error("Server connection failed")
	
	// 3. Prepare the Client-Side Struct
	// We create it now, but we don't have an FD (ID) for it yet.
	new_file_struct := SandstoreFD{
	    InodeID:  inode_id,
	    FilePath: filepath,
	    Mode:     mode,
	    Offset:   0,
	    Buffer:   EmptyBuffer(),
	    Mu:       Mutex(), // Per-file lock
	}
	
	// 4. Critical Section: Update the Global Table
	global_client.TableMu.Lock() // LOCK ACQUIRED
	defer global_client.TableMu.Unlock()
	
	// 5. Find Next Lowest Free FD
	// We start at 3 (0,1,2 are reserved for stdin/out/err)
	candidate_fd := 3
	for {
	    if global_client.openFiles[candidate_fd] == nil:
	        // Found a hole!
	        break
	    }
	    candidate_fd++
	    // Optional: Check for max open files limit here
	}
	
	// 6. Insert into Table
	new_file_struct.ID = candidate_fd
	global_client.openFiles[candidate_fd] = &new_file_struct
	
	// 7. Return the integer handle
	return candidate_fd, nil
```

### 2. library.Read(fd, n)

**Inputs:**

- `fd` (int): The file descriptor index.
- `n` (int): The number of bytes to read.

**Logic Flow:**

```cpp
function Read(fd, n) returns ([]byte, error):

    // 1. FD Resolution (Read-Lock the Table)
    // We only need a Read lock on the global table because we are just looking up a pointer.
    global_client.TableMu.RLock()
    file_struct := global_client.openFiles[fd]
    global_client.TableMu.RUnlock() 

    // 2. Validation
    if file_struct == nil:
        return nil, Error("Bad File Descriptor")

    // 3. File-Level Critical Section (Exclusive Lock)
    // We must lock the struct to ensure no one else changes the Offset while we read.
    file_struct.Mu.Lock()
    defer file_struct.Mu.Unlock()

    // 4. Permission Check
    if file_struct.Mode == WRITE_ONLY:
        return nil, Error("File not open for reading")

    // 5. Server Interaction
    // We pass the Inode and the CURRENT Offset (managed locally).
    // Note: The Server handles 'atime' updates.
    data, err := server.Read(file_struct.InodeID, file_struct.Offset, n)

    if err != nil:
        // Handle EOF: usually returned as 0 bytes or specific error, depending on server RPC
        return nil, err 

    // 6. Update State
    // Crucial: We must advance the offset by the actual number of bytes received.
    bytes_read := len(data)
    file_struct.Offset += bytes_read

    return data, nil
```

### 3. library.Write(fd, data)

**Inputs:**

- `fd` (int): The file descriptor index.
- `data` ([]byte): The bytes to write.

```go
function Write(fd, data) returns (int, error):

    // 1. FD Resolution (Read-Lock the Table)
    global_client.TableMu.RLock()
    file_struct := global_client.openFiles[fd]
    global_client.TableMu.RUnlock()

    // 2. Validation
    if file_struct == nil:
        return 0, Error("Bad File Descriptor")

    // 3. File-Level Critical Section (Exclusive Lock)
    file_struct.Mu.Lock()
    defer file_struct.Mu.Unlock()

    // 4. Permission Check
    if file_struct.Mode == READ_ONLY:
        return 0, Error("File not open for writing")

    // 5. Buffer Logic
    // Check if adding this new data would overflow the buffer limit.
    if len(file_struct.Buffer) + len(data) > MAX_BUFFER_SIZE:
        // FLUSH: The buffer is full. We must write the EXISTING buffer to the server.
        // Calculate where this buffered data belongs in the file.
        // (Current Offset points to the end of the buffer, so we subtract buffer length).
        flush_offset := file_struct.Offset - len(file_struct.Buffer)
        
        err := server.Write(file_struct.InodeID, flush_offset, file_struct.Buffer)
        if err != nil:
            return 0, err // Write failed
            
        // Clear the buffer after a successful write
        file_struct.Buffer = EmptyBuffer() 
    }

    // 6. Append to Buffer
    // Now that we've made space (if needed), we append the NEW data to the buffer.
    file_struct.Buffer.append(data)

    // 7. Update State
    // Advance the offset so the next write/read happens at the correct position.
    // Note: The data is in memory, but the offset reflects the logical file position.
    written_bytes := len(data)
    file_struct.Offset += written_bytes

    return written_bytes, nil
```

### 4. library.Fsync(fd)

**Inputs:**

- `fd` (int): The file descriptor index.

**Logic Flow:**

```go
function Fsync(fd) returns error:

    // 1. FD Resolution
    // We need to look up the struct, so we still need a quick Read-Lock on the table.
    global_client.TableMu.RLock()
    file_struct := global_client.openFiles[fd]
    global_client.TableMu.RUnlock()

    // 2. Validation
    if file_struct == nil:
        return Error("Bad File Descriptor")

    // 3. File-Level Critical Section
    // Exclusive lock ensures no one appends to the buffer while we are flushing it.
    file_struct.Mu.Lock()
    defer file_struct.Mu.Unlock()

    // 4. Check Buffer State
    // If the buffer is empty, there is nothing to persist.
    if len(file_struct.Buffer) == 0:
        return nil

    // 5. Server Interaction (The Flush)
    // We write the buffered data to the server at the calculated offset.
    // Logic: The 'Offset' in the struct points to the *end* of the buffer (logical position).
    // So the write must start at (Offset - BufferSize).
    flush_offset := file_struct.Offset - len(file_struct.Buffer)

    // This call blocks until the server (Raft) confirms persistence.
    err := server.Write(file_struct.InodeID, flush_offset, file_struct.Buffer)

    if err != nil:
        return err // Persistence failed

    // 6. Cleanup
    // Clear the buffer now that data is safely on the server.
    file_struct.Buffer = EmptyBuffer()

    return nil
```

### 5. library.Close(fd)

**Inputs:**

- `fd` (int): The file descriptor index.

**Logic Flow:**

```go
function Close(fd) returns error:

    // 1. Remove from Global Table (Write Lock)
    // We acquire the WRITE lock immediately because we intend to modify the map.
    global_client.TableMu.Lock()
    
    file_struct := global_client.openFiles[fd]
    
    if file_struct == nil:
        global_client.TableMu.Unlock()
        return Error("Bad File Descriptor")

    // DELETE the entry.
    // Now, no other thread can call Read/Write on this FD.
    delete(global_client.openFiles, fd)
    
    global_client.TableMu.Unlock() // Release global lock immediately.

    // 2. File-Level Critical Section (The "Decommissioning" Phase)
    // We still hold the 'file_struct' pointer locally, so it's safe to use.
    // Locking here waits for any currently running Read/Write operations to finish.
    file_struct.Mu.Lock()
    defer file_struct.Mu.Unlock()

    // 3. Final Flush
    // If there is data left in the buffer, we must persist it.
    if len(file_struct.Buffer) > 0:
        // Reuse logic from Write/Fsync
        flush_offset := file_struct.Offset - len(file_struct.Buffer)
        err := server.Write(file_struct.InodeID, flush_offset, file_struct.Buffer)
        
        if err != nil:
            // This is tricky. The file is closed locally, but the write failed.
            // Standard POSIX implies we should return the error.
            return err 
    
    // 4. Release Resources
    // In C/C++, we would free(file_struct). 
    // In Go, we just return. The GC will clean up file_struct since the map no longer references it.
    
    return nil
```

### 6. library.Remove(filepath)

**Inputs:**

- `filepath` (string): Absolute path to the file.

**Logic Flow:**

```go
function Remove(filepath) returns error:

    // 1. Global Critical Section (Big Lock)
    // You specified we should hold the table lock during the entire operation.
    // This is safe (prevents race conditions) but blocks ALL other clients during the RPC.
    global_client.TableMu.Lock()
    defer global_client.TableMu.Unlock()

    // 2. Search for Open FD (The "Eager Close" Logic)
    // We iterate the map to see if we have this file open locally.
    var target_fd = -1
    var target_struct = nil

    for fd, file_struct := range global_client.openFiles:
        if file_struct.FilePath == filepath:
            target_fd = fd
            target_struct = file_struct
            break
    
    // 3. Resolve Inode
    var inode_id uint64
    if target_struct != nil:
        // Best Case: We have it open, so we know the Inode ID without a lookup.
        inode_id = target_struct.InodeID
        
        // "Force Close" Step 1: Lock the struct so no one can Read/Write
        target_struct.Mu.Lock()
        // We don't unlock here because we are about to destroy it.
    else:
        // Common Case: File is not open locally. We must ask server for ID (or just delete by path).
        // Assuming Server.Delete takes an Inode ID based on your prompt:
        id, err := server.Lookup(filepath)
        if err != nil:
            return err // File doesn't exist
        inode_id = id

    // 4. Server Interaction (Delete)
    // We do this matching your instruction: "Delete from server first".
    err := server.Delete(inode_id)
    if err != nil:
        // If server failed, we probably shouldn't close the local FD.
        if target_struct != nil:
            target_struct.Mu.Unlock()
        return err

    // 5. Cleanup Local State
    // If we found an open FD, we destroy it now.
    if target_fd != -1:
        delete(global_client.openFiles, target_fd)
        target_struct.Mu.Unlock() // Release the lock on the now-detached struct
    
    return nil
```

### 7. library.Rename(src, dst)

**Inputs:**

- `src` (string): The current absolute path.
- `dst` (string): The new absolute path.

**Logic Flow:**

```go
function Rename(src, dst) returns error:

    // 1. Global Critical Section (The "World Freeze")
    // You requested locking the entire table during the call.
    // This prevents any other thread from Opening/Removing 'src' or 'dst' 
    // while the server is processing the move.
    global_client.TableMu.Lock()
    defer global_client.TableMu.Unlock()

    // 2. Server Interaction (The Source of Truth)
    // The server handles the transactional complexity (Raft log, atomicity).
    // We just block and wait for the leader to confirm it's done.
    err := server.Rename(src, dst)
    
    if err != nil:
        return err // Rename failed (e.g., src not found, or not leader)

    // 3. Update Client State (Reflecting the Reality)
    // If the server succeeded, we must update any open file descriptors 
    // that were pointing to 'src' so they now point to 'dst'.
    // Note: We do NOT update FDs pointing to 'dst'. If 'dst' was overwritten, 
    // those FDs continue to point to the old (now unlinked) inode, which is correct POSIX behavior.
    
    for fd, file_struct := range global_client.openFiles:
        if file_struct.FilePath == src:
            // Update the path in memory so it matches the new name.
            // We need the file-level lock just to update the string safely 
            // (in case a Read is checking the path for some reason).
            file_struct.Mu.Lock()
            file_struct.FilePath = dst
            file_struct.Mu.Unlock()
            
    return nil
```

### 8. library.Mkdir(path, mode)

**Inputs:**

- `path` (string): Absolute path of the new directory.
- `mode` (int): Permission bits.

**Logic Flow:**

```go
function Mkdir(path, mode) returns error:

    // 1. Path Parsing
    // The RPC requires {ParentID, Name}, so we must split the path.
    parent_path, dir_name := SplitPath(path)

    // 2. Resolve Parent Inode
    // We need the ID of the directory *containing* the new folder.
    // Note: We do NOT hold any client-side locks here because we aren't touching the FD table.
    parent_id, err := server.Lookup(parent_path)
    
    if err != nil:
        return Error("Parent directory not found")

    // 3. Server Interaction (Raft Replicated)
    // We send the creation request.
    // Guarantee: This is atomic on the leader.
    err = server.Mkdir(parent_id, dir_name, mode)

    if err != nil:
        // Handle "ErrNotLeader" by retrying (if policy allows) or returning error.
        // Handle "ErrNotFound" if parent disappeared between Lookup and Mkdir.
        return err

    return nil
```

### 9. library.Rmdir(path)

**Inputs:**

- `path` (string): Absolute path of the directory to remove.

**Logic Flow:**

```go
function Rmdir(path) returns error:

    // 1. Path Parsing
    parent_path, dir_name := SplitPath(path)

    // 2. Resolve Parent Inode
    parent_id, err := server.Lookup(parent_path)
    if err != nil:
        return Error("Parent directory not found")

    // 3. Server Interaction (Raft Replicated)
    // The server checks if the directory is empty before deleting.
    err = server.Rmdir(parent_id, dir_name)

    if err != nil:
        // Common errors: ErrNotEmpty (directory has files), ErrNotFound.
        return err

    return nil
```

### 10. library.ListDir(path)

**Inputs:**

- `path` (string): Absolute path.

**Logic Flow:**

```go
function ListDir(path) returns ([]DirEntry, error):

    // 1. Resolve Target Inode
    inode_id, err := server.Lookup(path)
    if err != nil:
        return nil, err

    // 2. Pagination Loop
    // The RPC supports pagination (Cookies). We want to fetch ALL entries for the user.
    var all_entries []DirEntry
    var cookie = 0
    var eof = false
    const BATCH_SIZE = 100

    for !eof:
        // Note: This is a LOCAL READ (Eventual Consistency).
        // It reads from the local state of the node we are talking to.
        // It does not go through Raft log.
        response, err := server.ReadDir(inode_id, cookie, BATCH_SIZE)
        
        if err != nil:
            return nil, err

        all_entries = append(all_entries, response.Entries...)
        
        // Update state for next iteration
        cookie = response.Cookie
        eof = response.EOF

    return all_entries, nil
```

### 11. library.Stat(path)

**Inputs:**

- `path` (string): Absolute path.

**Logic Flow:**

```go
function Stat(path) returns (FileAttributes, error):

    // 1. Resolve Target Inode
    inode_id, err := server.Lookup(path)
    if err != nil:
        return nil, err

    // 2. Server Interaction (Local Read)
    // Like ListDir, this is eventually consistent. 
    // It returns the attributes (Size, Mode, Mtime) as known by this specific node.
    attrs, err := server.GetAttr(inode_id)

    if err != nil:
        return nil, err

    return attrs, nil
```

---

## Stack

- The stack is already decided; the entire project is in Go, so Sandlib will first be created in Go, and if I want to, I will create stub libraries for other programming languages as well. But not right now.