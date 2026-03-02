package bolt

import (
	"bytes"
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
