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
