package bolt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	inmemoryms "github.com/AnishMulay/sandstore/internal/metadata_service/inmemory"
	"go.etcd.io/bbolt"
)

var (
	metadataStateBucket = []byte("metadata_state")
	superblockBucket    = []byte("superblock")
	inodesBucket        = []byte("inodes")
	dentriesBucket      = []byte("dentries")
	chunkMapBucket      = []byte("chunk_map")

	consistentIndexKey = []byte("consistent_index")
	superblockKey      = []byte("sb")
)

var requiredBuckets = [][]byte{
	metadataStateBucket,
	superblockBucket,
	inodesBucket,
	dentriesBucket,
	chunkMapBucket,
}

type BoltMetadataService struct {
	db       *bbolt.DB
	filePath string
}

func NewBoltMetadataService(filePath string) (*BoltMetadataService, error) {
	if filePath == "" {
		return nil, fmt.Errorf("bolt metadata service path cannot be empty")
	}

	dir := filepath.Dir(filePath)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create bolt metadata directory %q: %w", dir, err)
		}
	}

	db, err := bbolt.Open(filePath, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open bolt metadata db %q: %w", filePath, err)
	}

	svc := &BoltMetadataService{
		db:       db,
		filePath: filePath,
	}

	if err := db.Update(func(tx *bbolt.Tx) error {
		for _, bucketName := range requiredBuckets {
			if _, err := tx.CreateBucketIfNotExists(bucketName); err != nil {
				return fmt.Errorf("ensure bucket %q: %w", bucketName, err)
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	return svc, nil
}

func (s *BoltMetadataService) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *BoltMetadataService) ApplyCreate(op pms.MetadataOperation, consistentIndex uint64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}

	parentKey, err := encodeInodeKey(op.ParentID)
	if err != nil {
		return fmt.Errorf("encode parent inode id %q: %w", op.ParentID, err)
	}

	childKey, err := encodeInodeKey(op.InodeID)
	if err != nil {
		return fmt.Errorf("encode child inode id %q: %w", op.InodeID, err)
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		inodes := tx.Bucket(inodesBucket)
		dentries := tx.Bucket(dentriesBucket)
		metadataState := tx.Bucket(metadataStateBucket)
		if inodes == nil || dentries == nil || metadataState == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		parentBytes := inodes.Get(parentKey)
		if parentBytes == nil {
			return inmemoryms.ErrNotFound
		}

		var parent pms.Inode
		if err := decodeGob(parentBytes, &parent); err != nil {
			return fmt.Errorf("decode parent inode %q: %w", op.ParentID, err)
		}
		if parent.Type != pms.TypeDirectory {
			return inmemoryms.ErrNotFound
		}

		dentryKey := encodeDentryKey(parentKey, op.Name)
		if dentries.Get(dentryKey) != nil {
			return inmemoryms.ErrAlreadyExists
		}

		now := time.Unix(0, op.Timestamp)
		child := pms.Inode{
			InodeID:    op.InodeID,
			Type:       op.FileType,
			LinkCount:  1,
			Mode:       op.Mode,
			OwnerUID:   op.UID,
			OwnerGID:   op.GID,
			AccessTime: now,
			ModifyTime: now,
			ChangeTime: now,
		}
		if op.FileType == pms.TypeDirectory {
			child.LinkCount = 2
			parent.LinkCount++
		}

		childBytes, err := encodeGob(storableInode(child))
		if err != nil {
			return fmt.Errorf("encode child inode %q: %w", op.InodeID, err)
		}
		if err := inodes.Put(childKey, childBytes); err != nil {
			return fmt.Errorf("store child inode %q: %w", op.InodeID, err)
		}

		if err := dentries.Put(dentryKey, childKey); err != nil {
			return fmt.Errorf("store dentry %q under parent %q: %w", op.Name, op.ParentID, err)
		}

		parent.ModifyTime = now
		parent.ChangeTime = now

		parentBytes, err = encodeGob(storableInode(parent))
		if err != nil {
			return fmt.Errorf("encode parent inode %q: %w", op.ParentID, err)
		}
		if err := inodes.Put(parentKey, parentBytes); err != nil {
			return fmt.Errorf("update parent inode %q: %w", op.ParentID, err)
		}

		if err := metadataState.Put(consistentIndexKey, encodeUint64LE(consistentIndex)); err != nil {
			return fmt.Errorf("update consistent index %d: %w", consistentIndex, err)
		}

		return nil
	})
}

func (s *BoltMetadataService) ApplyRename(op pms.MetadataOperation, consistentIndex uint64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}

	srcParentKey, err := encodeInodeKey(op.ParentID)
	if err != nil {
		return fmt.Errorf("encode source parent inode id %q: %w", op.ParentID, err)
	}

	dstParentKey, err := encodeInodeKey(op.DstParentID)
	if err != nil {
		return fmt.Errorf("encode destination parent inode id %q: %w", op.DstParentID, err)
	}

	srcDentryKey := encodeDentryKey(srcParentKey, op.Name)
	dstDentryKey := encodeDentryKey(dstParentKey, op.DstName)

	return s.db.Update(func(tx *bbolt.Tx) error {
		inodes := tx.Bucket(inodesBucket)
		dentries := tx.Bucket(dentriesBucket)
		metadataState := tx.Bucket(metadataStateBucket)
		if inodes == nil || dentries == nil || metadataState == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		srcParentBytes := inodes.Get(srcParentKey)
		if srcParentBytes == nil {
			return inmemoryms.ErrNotFound
		}

		dstParentBytes := inodes.Get(dstParentKey)
		if dstParentBytes == nil {
			return inmemoryms.ErrNotFound
		}

		var srcParent pms.Inode
		if err := decodeGob(srcParentBytes, &srcParent); err != nil {
			return fmt.Errorf("decode source parent inode %q: %w", op.ParentID, err)
		}

		var dstParent pms.Inode
		if err := decodeGob(dstParentBytes, &dstParent); err != nil {
			return fmt.Errorf("decode destination parent inode %q: %w", op.DstParentID, err)
		}

		childKey := dentries.Get(srcDentryKey)
		if childKey == nil {
			return inmemoryms.ErrNotFound
		}

		childKey = append([]byte(nil), childKey...)

		if err := dentries.Delete(srcDentryKey); err != nil {
			return fmt.Errorf("delete source dentry %q under parent %q: %w", op.Name, op.ParentID, err)
		}

		if overwrittenChildKey := dentries.Get(dstDentryKey); overwrittenChildKey != nil {
			overwrittenChildKey = append([]byte(nil), overwrittenChildKey...)
			if err := inodes.Delete(overwrittenChildKey); err != nil {
				return fmt.Errorf("delete overwritten inode at destination %q/%q: %w", op.DstParentID, op.DstName, err)
			}
		}

		if err := dentries.Put(dstDentryKey, childKey); err != nil {
			return fmt.Errorf("store destination dentry %q under parent %q: %w", op.DstName, op.DstParentID, err)
		}

		now := time.Unix(0, op.Timestamp)
		srcParent.ModifyTime = now
		srcParent.ChangeTime = now
		dstParent.ModifyTime = now
		dstParent.ChangeTime = now

		srcParentBytes, err = encodeGob(storableInode(srcParent))
		if err != nil {
			return fmt.Errorf("encode source parent inode %q: %w", op.ParentID, err)
		}
		if err := inodes.Put(srcParentKey, srcParentBytes); err != nil {
			return fmt.Errorf("update source parent inode %q: %w", op.ParentID, err)
		}

		dstParentBytes, err = encodeGob(storableInode(dstParent))
		if err != nil {
			return fmt.Errorf("encode destination parent inode %q: %w", op.DstParentID, err)
		}
		if err := inodes.Put(dstParentKey, dstParentBytes); err != nil {
			return fmt.Errorf("update destination parent inode %q: %w", op.DstParentID, err)
		}

		if err := metadataState.Put(consistentIndexKey, encodeUint64LE(consistentIndex)); err != nil {
			return fmt.Errorf("update consistent index %d: %w", consistentIndex, err)
		}

		return nil
	})
}

func (s *BoltMetadataService) ApplyRemove(op pms.MetadataOperation, consistentIndex uint64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}

	parentKey, err := encodeInodeKey(op.ParentID)
	if err != nil {
		return fmt.Errorf("encode parent inode id %q: %w", op.ParentID, err)
	}

	dentryKey := encodeDentryKey(parentKey, op.Name)

	return s.db.Update(func(tx *bbolt.Tx) error {
		inodes := tx.Bucket(inodesBucket)
		dentries := tx.Bucket(dentriesBucket)
		metadataState := tx.Bucket(metadataStateBucket)
		if inodes == nil || dentries == nil || metadataState == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		childKey := dentries.Get(dentryKey)
		if childKey == nil {
			return inmemoryms.ErrNotFound
		}
		childKey = append([]byte(nil), childKey...)

		parentBytes := inodes.Get(parentKey)
		if parentBytes == nil {
			return inmemoryms.ErrNotFound
		}

		var parent pms.Inode
		if err := decodeGob(parentBytes, &parent); err != nil {
			return fmt.Errorf("decode parent inode %q: %w", op.ParentID, err)
		}

		childBytes := inodes.Get(childKey)
		if childBytes == nil {
			return inmemoryms.ErrNotFound
		}

		var child pms.Inode
		if err := decodeGob(childBytes, &child); err != nil {
			return fmt.Errorf("decode child inode for parent %q name %q: %w", op.ParentID, op.Name, err)
		}

		if err := dentries.Delete(dentryKey); err != nil {
			return fmt.Errorf("delete dentry %q under parent %q: %w", op.Name, op.ParentID, err)
		}
		if err := inodes.Delete(childKey); err != nil {
			return fmt.Errorf("delete child inode for parent %q name %q: %w", op.ParentID, op.Name, err)
		}

		now := time.Unix(0, op.Timestamp)
		if child.Type == pms.TypeDirectory {
			parent.LinkCount--
		}
		parent.ModifyTime = now
		parent.ChangeTime = now

		parentBytes, err = encodeGob(storableInode(parent))
		if err != nil {
			return fmt.Errorf("encode parent inode %q: %w", op.ParentID, err)
		}
		if err := inodes.Put(parentKey, parentBytes); err != nil {
			return fmt.Errorf("update parent inode %q: %w", op.ParentID, err)
		}

		if err := metadataState.Put(consistentIndexKey, encodeUint64LE(consistentIndex)); err != nil {
			return fmt.Errorf("update consistent index %d: %w", consistentIndex, err)
		}

		return nil
	})
}

func (s *BoltMetadataService) ReadDir(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntry, int, bool, error) {
	if s == nil || s.db == nil {
		return nil, 0, false, fmt.Errorf("bolt metadata service is not initialized")
	}

	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}

	parentKey, err := encodeInodeKey(inodeID)
	if err != nil {
		return nil, 0, false, fmt.Errorf("encode parent inode id %q: %w", inodeID, err)
	}

	prefix := append([]byte(nil), parentKey...)
	entries := make([]pms.DirEntry, 0)

	err = s.db.View(func(tx *bbolt.Tx) error {
		inodes := tx.Bucket(inodesBucket)
		dentries := tx.Bucket(dentriesBucket)
		if inodes == nil || dentries == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		parentBytes := inodes.Get(parentKey)
		if parentBytes == nil {
			return inmemoryms.ErrNotFound
		}

		var parent pms.Inode
		if err := decodeGob(parentBytes, &parent); err != nil {
			return fmt.Errorf("decode parent inode %q: %w", inodeID, err)
		}
		if parent.Type != pms.TypeDirectory {
			return inmemoryms.ErrNotDir
		}

		cursor := dentries.Cursor()
		for key, value := cursor.Seek(prefix); key != nil && bytes.HasPrefix(key, prefix); key, value = cursor.Next() {
			if err := ctx.Err(); err != nil {
				return err
			}

			childBytes := inodes.Get(value)
			if childBytes == nil {
				return fmt.Errorf("missing child inode for dentry parent=%q name=%q", inodeID, string(key[len(prefix):]))
			}

			var child pms.Inode
			if err := decodeGob(childBytes, &child); err != nil {
				return fmt.Errorf("decode child inode for dentry parent=%q name=%q: %w", inodeID, string(key[len(prefix):]), err)
			}

			entries = append(entries, pms.DirEntry{
				Name:    string(key[len(prefix):]),
				InodeID: child.InodeID,
				Type:    child.Type,
			})
		}

		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}

	if cookie < 0 {
		cookie = 0
	}

	if cookie >= len(entries) {
		return []pms.DirEntry{}, cookie, true, nil
	}

	if maxEntries <= 0 {
		maxEntries = len(entries) - cookie
	}

	end := cookie + maxEntries
	if end > len(entries) {
		end = len(entries)
	}

	return entries[cookie:end], end, end == len(entries), nil
}

func (s *BoltMetadataService) Recover() (uint64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("bolt metadata service is not initialized")
	}

	var consistentIndex []byte
	if err := s.db.View(func(tx *bbolt.Tx) error {
		metadataState := tx.Bucket(metadataStateBucket)
		if metadataState == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		indexBytes := metadataState.Get(consistentIndexKey)
		if indexBytes != nil {
			consistentIndex = append([]byte(nil), indexBytes...)
		}
		return nil
	}); err != nil {
		return 0, err
	}

	if consistentIndex != nil {
		if len(consistentIndex) != 8 {
			return 0, fmt.Errorf("invalid consistent index length: got %d", len(consistentIndex))
		}
		return binary.LittleEndian.Uint64(consistentIndex), nil
	}

	now := time.Now()
	rootInodeID := "0"
	superblock := pms.Superblock{
		FsID:            "00000000-0000-0000-0000-000000000000",
		RootInodeID:     rootInodeID,
		ChunkSize:       8 * 1024 * 1024,
		MaxFilenameSize: 255,
		MaxFileSize:     1 << 40,
		CreatedAt:       now,
	}
	rootInode := pms.Inode{
		InodeID:    rootInodeID,
		Type:       pms.TypeDirectory,
		LinkCount:  2,
		Mode:       0o755,
		AccessTime: now,
		ModifyTime: now,
		ChangeTime: now,
	}

	rootKey, err := encodeInodeKey(rootInodeID)
	if err != nil {
		return 0, fmt.Errorf("encode root inode id %q: %w", rootInodeID, err)
	}

	if err := s.db.Update(func(tx *bbolt.Tx) error {
		metadataState := tx.Bucket(metadataStateBucket)
		superblockBucket := tx.Bucket(superblockBucket)
		inodes := tx.Bucket(inodesBucket)
		if metadataState == nil || superblockBucket == nil || inodes == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		superblockBytes, err := encodeGob(superblock)
		if err != nil {
			return fmt.Errorf("encode superblock: %w", err)
		}
		if err := superblockBucket.Put(superblockKey, superblockBytes); err != nil {
			return fmt.Errorf("store superblock: %w", err)
		}

		rootInodeBytes, err := encodeGob(storableInode(rootInode))
		if err != nil {
			return fmt.Errorf("encode root inode: %w", err)
		}
		if err := inodes.Put(rootKey, rootInodeBytes); err != nil {
			return fmt.Errorf("store root inode: %w", err)
		}

		if err := metadataState.Put(consistentIndexKey, encodeUint64LE(0)); err != nil {
			return fmt.Errorf("initialize consistent index: %w", err)
		}

		return nil
	}); err != nil {
		return 0, err
	}

	return 0, nil
}

func encodeInodeKey(inodeID string) ([]byte, error) {
	parsed, err := strconv.ParseUint(inodeID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("inode id must be a base-10 uint64: %w", err)
	}

	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, parsed)
	return key, nil
}

func encodeDentryKey(parentKey []byte, name string) []byte {
	key := make([]byte, 0, len(parentKey)+len(name))
	key = append(key, parentKey...)
	key = append(key, name...)
	return key
}

func encodeUint64LE(value uint64) []byte {
	out := make([]byte, 8)
	binary.LittleEndian.PutUint64(out, value)
	return out
}

func encodeGob(value any) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(value); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeGob(data []byte, target any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(target)
}

func storableInode(inode pms.Inode) pms.Inode {
	inode.Children = nil
	inode.ChunkList = nil
	return inode
}
