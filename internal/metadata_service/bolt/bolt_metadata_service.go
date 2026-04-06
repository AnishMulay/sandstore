package bolt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	pmr "github.com/AnishMulay/sandstore/internal/metadata_replicator"
	rr "github.com/AnishMulay/sandstore/internal/metadata_replicator/raft_replicator"
	pms "github.com/AnishMulay/sandstore/internal/metadata_service"
	inmemoryms "github.com/AnishMulay/sandstore/internal/metadata_service/inmemory"
	"github.com/AnishMulay/sandstore/internal/metrics"
	"github.com/AnishMulay/sandstore/topology/contract"
	"github.com/google/uuid"
	"go.etcd.io/bbolt"
)

var (
	metadataStateBucket = []byte("metadata_state")
	superblockBucket    = []byte("superblock")
	inodesBucket        = []byte("inodes")
	dentriesBucket      = []byte("dentries")
	chunkMapBucket      = []byte("chunk_map")
	chunkPlacementsBkt  = []byte("chunk_placements")
	chunkIntentsBkt     = []byte("chunk_intents")

	consistentIndexKey = []byte("consistent_index")
	superblockKey      = []byte("sb")
)

var requiredBuckets = [][]byte{
	metadataStateBucket,
	superblockBucket,
	inodesBucket,
	dentriesBucket,
	chunkMapBucket,
	chunkPlacementsBkt,
	chunkIntentsBkt,
}

var inodeIDCounter atomic.Uint64

type BoltMetadataService struct {
	db             *bbolt.DB
	filePath       string
	replicator     pmr.MetadataReplicator
	metricsService metrics.MetricsService
}

func NewBoltMetadataService(filePath string, metricsService metrics.MetricsService) (*BoltMetadataService, error) {
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
		db:             db,
		filePath:       filePath,
		metricsService: metricsService,
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

func (s *BoltMetadataService) SetReplicator(replicator pmr.MetadataReplicator) {
	if s == nil {
		return
	}
	s.replicator = replicator
}

func (s *BoltMetadataService) Start() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}
	if _, err := s.Recover(); err != nil {
		return err
	}
	if s.replicator == nil {
		return fmt.Errorf("bolt metadata replicator is not configured")
	}
	return s.replicator.Start(s.ApplyTransaction)
}

func (s *BoltMetadataService) Stop() error {
	if s == nil {
		return nil
	}

	var repErr error
	if s.replicator != nil {
		repErr = s.replicator.Stop()
	}

	dbErr := s.Close()
	if repErr != nil {
		return repErr
	}
	return dbErr
}

func (s *BoltMetadataService) ApplyTransaction(data []byte) error {
	var envelope rr.RaftCommandEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}

	switch envelope.Type {
	case rr.PayloadPosixMeta:
		return s.applyStandardMetadata(envelope.PosixMeta)
	case rr.PayloadChunkIntent:
		return s.applyChunkIntentAtomic(envelope.ChunkIntent, envelope.PosixMeta)
	default:
		return fmt.Errorf("unknown envelope type: %v", envelope.Type)
	}
}

func (s *BoltMetadataService) applyStandardMetadata(op *pms.MetadataOperation) error {
	if op == nil {
		return fmt.Errorf("missing metadata operation in raft envelope")
	}

	consistentIndex, err := s.nextConsistentIndex()
	if err != nil {
		return err
	}

	switch op.Type {
	case pms.OpCreate:
		return s.ApplyCreate(*op, consistentIndex)
	case pms.OpRemove:
		return s.ApplyRemove(*op, consistentIndex)
	case pms.OpRename:
		return s.ApplyRename(*op, consistentIndex)
	case pms.OpSetAttr:
		return s.ApplySetAttr(*op, consistentIndex)
	case pms.OpUpdateInode:
		return s.ApplyUpdateInode(*op, consistentIndex)
	default:
		return fmt.Errorf("unknown operation type: %v", op.Type)
	}
}

func (s *BoltMetadataService) applyChunkIntentAtomic(intent *rr.ChunkIntentOperation, metaUpdate *pms.MetadataOperation) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}
	if intent == nil {
		return fmt.Errorf("missing chunk intent operation in raft envelope")
	}

	consistentIndex, err := s.nextConsistentIndex()
	if err != nil {
		return err
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		intentsBucket, err := tx.CreateBucketIfNotExists(chunkIntentsBkt)
		if err != nil {
			return err
		}

		intentBytes, err := json.Marshal(intent)
		if err != nil {
			return fmt.Errorf("marshal chunk intent: %w", err)
		}
		if err := intentsBucket.Put([]byte(intent.TxnID), intentBytes); err != nil {
			return err
		}

		placementBucket, err := tx.CreateBucketIfNotExists(chunkPlacementsBkt)
		if err != nil {
			return err
		}

		placementBytes, err := json.Marshal(intent.NodeIDs)
		if err != nil {
			return fmt.Errorf("marshal chunk placement: %w", err)
		}
		if err := placementBucket.Put([]byte(intent.ChunkID), placementBytes); err != nil {
			return err
		}

		if metaUpdate != nil && metaUpdate.Type == pms.OpUpdateInode {
			inodes := tx.Bucket(inodesBucket)
			chunkMap := tx.Bucket(chunkMapBucket)
			if inodes == nil || chunkMap == nil {
				return fmt.Errorf("bolt metadata schema is not initialized")
			}

			inodeKey, err := encodeInodeKey(metaUpdate.InodeID)
			if err != nil {
				return fmt.Errorf("encode inode id %q: %w", metaUpdate.InodeID, err)
			}

			inode, err := loadStoredInode(inodes, inodeKey, metaUpdate.InodeID)
			if err != nil {
				return err
			}

			if metaUpdate.NewSize != nil {
				inode.FileSize = *metaUpdate.NewSize
			}
			if metaUpdate.NewChunkList != nil {
				if err := replaceChunkList(chunkMap, inodeKey, metaUpdate.NewChunkList); err != nil {
					return fmt.Errorf("update chunk list for inode %q: %w", metaUpdate.InodeID, err)
				}
			}

			if metaUpdate.Timestamp > 0 {
				inode.ModifyTime = time.Unix(0, metaUpdate.Timestamp)
			} else {
				inode.ModifyTime = time.Now()
			}
			inode.ChangeTime = inode.ModifyTime

			inodeBytes, err := encodeGob(storableInode(*inode))
			if err != nil {
				return fmt.Errorf("encode inode %q: %w", metaUpdate.InodeID, err)
			}
			if err := inodes.Put(inodeKey, inodeBytes); err != nil {
				return fmt.Errorf("update inode %q: %w", metaUpdate.InodeID, err)
			}
		}

		metadataState := tx.Bucket(metadataStateBucket)
		if metadataState == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}
		if err := metadataState.Put(consistentIndexKey, encodeUint64LE(consistentIndex)); err != nil {
			return fmt.Errorf("update consistent index %d: %w", consistentIndex, err)
		}

		return nil
	})
}

func (s *BoltMetadataService) replicateOp(ctx context.Context, op pms.MetadataOperation) error {
	if s == nil || s.replicator == nil {
		return fmt.Errorf("bolt metadata replicator is not configured")
	}

	op.OpID = uuid.New().String()
	op.Timestamp = time.Now().UnixNano()

	envelope := rr.RaftCommandEnvelope{
		Type:      rr.PayloadPosixMeta,
		PosixMeta: &op,
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal op: %w", err)
	}

	return s.replicator.Replicate(ctx, data)
}

func (s *BoltMetadataService) nextConsistentIndex() (uint64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("bolt metadata service is not initialized")
	}

	var current uint64
	if err := s.db.View(func(tx *bbolt.Tx) error {
		metadataState := tx.Bucket(metadataStateBucket)
		if metadataState == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		indexBytes := metadataState.Get(consistentIndexKey)
		if indexBytes == nil {
			current = 0
			return nil
		}
		if len(indexBytes) != 8 {
			return fmt.Errorf("invalid consistent index length: got %d", len(indexBytes))
		}
		current = binary.LittleEndian.Uint64(indexBytes)
		return nil
	}); err != nil {
		return 0, err
	}

	return current + 1, nil
}

func (s *BoltMetadataService) ApplyCreate(op pms.MetadataOperation, consistentIndex uint64) error {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start).Seconds()
		tags := metrics.MetricTags{
			Operation:  "apply_create",
			Service:    "BoltMetadataService",
			Additional: nil,
		}
		if s != nil && s.metricsService != nil {
			s.metricsService.Observe(metrics.MetadataOperationLatency, elapsed, tags)
		}
	}()

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

	err = s.db.Update(func(tx *bbolt.Tx) error {
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
	return err
}

func (s *BoltMetadataService) ApplyRename(op pms.MetadataOperation, consistentIndex uint64) error {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start).Seconds()
		tags := metrics.MetricTags{
			Operation:  "apply_rename",
			Service:    "BoltMetadataService",
			Additional: nil,
		}
		if s != nil && s.metricsService != nil {
			s.metricsService.Observe(metrics.MetadataOperationLatency, elapsed, tags)
		}
	}()

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

	err = s.db.Update(func(tx *bbolt.Tx) error {
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
	return err
}

func (s *BoltMetadataService) ApplyRemove(op pms.MetadataOperation, consistentIndex uint64) error {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start).Seconds()
		tags := metrics.MetricTags{
			Operation:  "apply_remove",
			Service:    "BoltMetadataService",
			Additional: nil,
		}
		if s != nil && s.metricsService != nil {
			s.metricsService.Observe(metrics.MetadataOperationLatency, elapsed, tags)
		}
	}()

	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}

	parentKey, err := encodeInodeKey(op.ParentID)
	if err != nil {
		return fmt.Errorf("encode parent inode id %q: %w", op.ParentID, err)
	}

	dentryKey := encodeDentryKey(parentKey, op.Name)

	err = s.db.Update(func(tx *bbolt.Tx) error {
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
	return err
}

func (s *BoltMetadataService) ApplySetAttr(op pms.MetadataOperation, consistentIndex uint64) error {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start).Seconds()
		tags := metrics.MetricTags{
			Operation:  "apply_set_attr",
			Service:    "BoltMetadataService",
			Additional: nil,
		}
		if s != nil && s.metricsService != nil {
			s.metricsService.Observe(metrics.MetadataOperationLatency, elapsed, tags)
		}
	}()

	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}

	inodeKey, err := encodeInodeKey(op.InodeID)
	if err != nil {
		return fmt.Errorf("encode inode id %q: %w", op.InodeID, err)
	}

	err = s.db.Update(func(tx *bbolt.Tx) error {
		inodes := tx.Bucket(inodesBucket)
		metadataState := tx.Bucket(metadataStateBucket)
		if inodes == nil || metadataState == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		inode, err := loadStoredInode(inodes, inodeKey, op.InodeID)
		if err != nil {
			return err
		}

		now := time.Unix(0, op.Timestamp)
		if op.SetMode != nil {
			inode.Mode = *op.SetMode
		}
		if op.SetUID != nil {
			inode.OwnerUID = *op.SetUID
		}
		if op.SetGID != nil {
			inode.OwnerGID = *op.SetGID
		}
		if op.SetATime != nil {
			inode.AccessTime = time.Unix(0, *op.SetATime)
		}
		if op.SetMTime != nil {
			inode.ModifyTime = time.Unix(0, *op.SetMTime)
		}
		inode.ChangeTime = now

		inodeBytes, err := encodeGob(storableInode(*inode))
		if err != nil {
			return fmt.Errorf("encode inode %q: %w", op.InodeID, err)
		}
		if err := inodes.Put(inodeKey, inodeBytes); err != nil {
			return fmt.Errorf("update inode %q: %w", op.InodeID, err)
		}

		if err := metadataState.Put(consistentIndexKey, encodeUint64LE(consistentIndex)); err != nil {
			return fmt.Errorf("update consistent index %d: %w", consistentIndex, err)
		}

		return nil
	})
	return err
}

func (s *BoltMetadataService) ApplyUpdateInode(op pms.MetadataOperation, consistentIndex uint64) error {
	start := time.Now()
	defer func() {
		elapsed := time.Since(start).Seconds()
		tags := metrics.MetricTags{
			Operation:  "apply_update_inode",
			Service:    "BoltMetadataService",
			Additional: nil,
		}
		if s != nil && s.metricsService != nil {
			s.metricsService.Observe(metrics.MetadataOperationLatency, elapsed, tags)
		}
	}()

	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}

	inodeKey, err := encodeInodeKey(op.InodeID)
	if err != nil {
		return fmt.Errorf("encode inode id %q: %w", op.InodeID, err)
	}

	err = s.db.Update(func(tx *bbolt.Tx) error {
		inodes := tx.Bucket(inodesBucket)
		chunkMap := tx.Bucket(chunkMapBucket)
		metadataState := tx.Bucket(metadataStateBucket)
		if inodes == nil || chunkMap == nil || metadataState == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		inode, err := loadStoredInode(inodes, inodeKey, op.InodeID)
		if err != nil {
			return err
		}

		if op.NewSize != nil {
			inode.FileSize = *op.NewSize
		}
		if op.SetMTime != nil {
			inode.ModifyTime = time.Unix(0, *op.SetMTime)
		}
		inode.VersionNumber++
		inode.ChangeTime = time.Unix(0, op.Timestamp)

		inodeBytes, err := encodeGob(storableInode(*inode))
		if err != nil {
			return fmt.Errorf("encode inode %q: %w", op.InodeID, err)
		}
		if err := inodes.Put(inodeKey, inodeBytes); err != nil {
			return fmt.Errorf("update inode %q: %w", op.InodeID, err)
		}

		if op.NewChunkList != nil {
			if err := replaceChunkList(chunkMap, inodeKey, op.NewChunkList); err != nil {
				return fmt.Errorf("update chunk list for inode %q: %w", op.InodeID, err)
			}
		}

		if err := metadataState.Put(consistentIndexKey, encodeUint64LE(consistentIndex)); err != nil {
			return fmt.Errorf("update consistent index %d: %w", consistentIndex, err)
		}

		return nil
	})
	return err
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

func (s *BoltMetadataService) Lookup(ctx context.Context, parentInodeID string, name string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("bolt metadata service is not initialized")
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}

	parentKey, err := encodeInodeKey(parentInodeID)
	if err != nil {
		return "", fmt.Errorf("encode parent inode id %q: %w", parentInodeID, err)
	}

	dentryKey := encodeDentryKey(parentKey, name)
	var childID string

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
			return fmt.Errorf("decode parent inode %q: %w", parentInodeID, err)
		}
		if parent.Type != pms.TypeDirectory {
			return inmemoryms.ErrNotDir
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		childKey := dentries.Get(dentryKey)
		if childKey == nil {
			return inmemoryms.ErrNotFound
		}

		childID, err = decodeInodeID(childKey)
		if err != nil {
			return fmt.Errorf("decode child inode id for parent %q name %q: %w", parentInodeID, name, err)
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	return childID, nil
}

func (s *BoltMetadataService) GetAttributes(ctx context.Context, inodeID string) (*pms.Attributes, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("bolt metadata service is not initialized")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	inodeKey, err := encodeInodeKey(inodeID)
	if err != nil {
		return nil, fmt.Errorf("encode inode id %q: %w", inodeID, err)
	}

	var attrs *pms.Attributes
	err = s.db.View(func(tx *bbolt.Tx) error {
		inodes := tx.Bucket(inodesBucket)
		if inodes == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		inodeBytes := inodes.Get(inodeKey)
		if inodeBytes == nil {
			return inmemoryms.ErrNotFound
		}

		var inode pms.Inode
		if err := decodeGob(inodeBytes, &inode); err != nil {
			return fmt.Errorf("decode inode %q: %w", inodeID, err)
		}

		attrs = &pms.Attributes{
			InodeID:    inode.InodeID,
			Type:       inode.Type,
			Mode:       inode.Mode,
			Size:       inode.FileSize,
			AccessTime: inode.AccessTime,
			ModifyTime: inode.ModifyTime,
			ChangeTime: inode.ChangeTime,
			UID:        inode.OwnerUID,
			GID:        inode.OwnerGID,
		}

		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}

	return attrs, nil
}

func (s *BoltMetadataService) GetFsStat(ctx context.Context) (*pms.FileSystemStats, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("bolt metadata service is not initialized")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var stats *pms.FileSystemStats
	err := s.db.View(func(tx *bbolt.Tx) error {
		superblockBkt := tx.Bucket(superblockBucket)
		inodes := tx.Bucket(inodesBucket)
		if superblockBkt == nil || inodes == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		superblockBytes := superblockBkt.Get(superblockKey)
		if superblockBytes == nil {
			return inmemoryms.ErrNotFound
		}

		var superblock pms.Superblock
		if err := decodeGob(superblockBytes, &superblock); err != nil {
			return fmt.Errorf("decode superblock: %w", err)
		}

		stats = &pms.FileSystemStats{
			TotalSpace:  superblock.MaxFileSize,
			UsedSpace:   0,
			TotalInodes: superblock.MaxFileSize,
			UsedInodes:  int64(inodes.Stats().KeyN),
			BlockSize:   superblock.ChunkSize,
		}

		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}

	return stats, nil
}

func (s *BoltMetadataService) GetFsInfo(ctx context.Context) (*pms.FileSystemInfo, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("bolt metadata service is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var info *pms.FileSystemInfo
	err := s.db.View(func(tx *bbolt.Tx) error {
		superblock, err := loadSuperblock(tx)
		if err != nil {
			return err
		}

		info = &pms.FileSystemInfo{
			FsID:            superblock.FsID,
			MaxFileSize:     superblock.MaxFileSize,
			MaxFilenameSize: superblock.MaxFilenameSize,
			ChunkSize:       superblock.ChunkSize,
		}
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}

	return info, nil
}

func (s *BoltMetadataService) GetInode(ctx context.Context, inodeID string) (*pms.Inode, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("bolt metadata service is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	inodeKey, err := encodeInodeKey(inodeID)
	if err != nil {
		return nil, fmt.Errorf("encode inode id %q: %w", inodeID, err)
	}

	var inode *pms.Inode
	err = s.db.View(func(tx *bbolt.Tx) error {
		inodes := tx.Bucket(inodesBucket)
		chunkMap := tx.Bucket(chunkMapBucket)
		if inodes == nil || chunkMap == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		storedInode, err := loadStoredInode(inodes, inodeKey, inodeID)
		if err != nil {
			return err
		}

		chunkList, err := loadChunkList(chunkMap, inodeKey)
		if err != nil {
			return fmt.Errorf("load chunk list for inode %q: %w", inodeID, err)
		}
		storedInode.ChunkList = chunkList
		inode = storedInode
		return ctx.Err()
	})
	if err != nil {
		return nil, err
	}

	return inode, nil
}

func (s *BoltMetadataService) LookupPath(ctx context.Context, path string) (string, error) {
	if s == nil || s.db == nil {
		return "", fmt.Errorf("bolt metadata service is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	cleanParts := strings.Split(strings.Trim(path, "/"), "/")
	var currentID string
	err := s.db.View(func(tx *bbolt.Tx) error {
		superblock, err := loadSuperblock(tx)
		if err != nil {
			return err
		}

		inodes := tx.Bucket(inodesBucket)
		dentries := tx.Bucket(dentriesBucket)
		if inodes == nil || dentries == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		currentID = superblock.RootInodeID
		if path == "/" {
			return nil
		}
		for _, part := range cleanParts {
			if part == "" || part == "." {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			currentKey, err := encodeInodeKey(currentID)
			if err != nil {
				return fmt.Errorf("encode inode id %q: %w", currentID, err)
			}
			inode, err := loadStoredInode(inodes, currentKey, currentID)
			if err != nil {
				return err
			}
			if inode.Type != pms.TypeDirectory {
				return inmemoryms.ErrNotDir
			}

			childKey := dentries.Get(encodeDentryKey(currentKey, part))
			if childKey == nil {
				return inmemoryms.ErrNotFound
			}
			currentID, err = decodeInodeID(childKey)
			if err != nil {
				return fmt.Errorf("decode child inode id for path %q part %q: %w", path, part, err)
			}
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	return currentID, nil
}

func (s *BoltMetadataService) Access(ctx context.Context, inodeID string, uid, gid uint32, accessMask uint32) error {
	inode, err := s.GetInode(ctx, inodeID)
	if err != nil {
		return err
	}

	if uid == 0 {
		return nil
	}

	var granted uint32
	switch {
	case uid == inode.OwnerUID:
		granted = (inode.Mode >> 6) & 7
	case gid == inode.OwnerGID:
		granted = (inode.Mode >> 3) & 7
	default:
		granted = inode.Mode & 7
	}

	if granted&accessMask == accessMask {
		return nil
	}

	return fmt.Errorf("permission denied")
}

func (s *BoltMetadataService) ReadDirPlus(ctx context.Context, inodeID string, cookie int, maxEntries int) ([]pms.DirEntryPlus, int, bool, error) {
	entries, nextCookie, eof, err := s.ReadDir(ctx, inodeID, cookie, maxEntries)
	if err != nil {
		return nil, 0, false, err
	}

	plusEntries := make([]pms.DirEntryPlus, 0, len(entries))
	for _, entry := range entries {
		attrs, err := s.GetAttributes(ctx, entry.InodeID)
		if err != nil {
			return nil, 0, false, err
		}
		plusEntries = append(plusEntries, pms.DirEntryPlus{
			Name:    entry.Name,
			InodeID: entry.InodeID,
			Type:    entry.Type,
			Inode:   attrs,
		})
	}

	return plusEntries, nextCookie, eof, nil
}

func (s *BoltMetadataService) SetAttributes(ctx context.Context, inodeID string, mode *uint32, uid, gid *uint32, atime, mtime *int64) (*pms.Attributes, error) {
	if _, err := s.GetAttributes(ctx, inodeID); err != nil {
		return nil, err
	}

	op := pms.MetadataOperation{
		Type:     pms.OpSetAttr,
		InodeID:  inodeID,
		SetMode:  mode,
		SetUID:   uid,
		SetGID:   gid,
		SetATime: atime,
		SetMTime: mtime,
	}
	if err := s.replicateOp(ctx, op); err != nil {
		return nil, err
	}

	return s.GetAttributes(ctx, inodeID)
}

func (s *BoltMetadataService) UpdateInode(ctx context.Context, inodeID string, newSize int64, newChunkList []contract.ChunkDescriptor, mtime int64) error {
	if _, err := s.GetInode(ctx, inodeID); err != nil {
		return err
	}

	op := pms.MetadataOperation{
		Type:         pms.OpUpdateInode,
		InodeID:      inodeID,
		NewSize:      &newSize,
		NewChunkList: newChunkList,
		SetMTime:     &mtime,
	}
	return s.replicateOp(ctx, op)
}

func (s *BoltMetadataService) UpdateChunkPlacement(ctx context.Context, chunkID string, nodeIDs []string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		placementBucket, err := tx.CreateBucketIfNotExists(chunkPlacementsBkt)
		if err != nil {
			return err
		}

		placementBytes, err := json.Marshal(nodeIDs)
		if err != nil {
			return fmt.Errorf("marshal chunk placement: %w", err)
		}

		return placementBucket.Put([]byte(chunkID), placementBytes)
	})
}

func (s *BoltMetadataService) GetChunkPlacement(ctx context.Context, chunkID string) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("bolt metadata service is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var nodeIDs []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		placementBucket := tx.Bucket(chunkPlacementsBkt)
		if placementBucket == nil {
			return inmemoryms.ErrNotFound
		}

		placementBytes := placementBucket.Get([]byte(chunkID))
		if placementBytes == nil {
			return inmemoryms.ErrNotFound
		}

		if err := json.Unmarshal(placementBytes, &nodeIDs); err != nil {
			return fmt.Errorf("unmarshal chunk placement: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return append([]string(nil), nodeIDs...), nil
}

func (s *BoltMetadataService) GetIntentState(ctx context.Context, txnID string) (pms.IntentState, error) {
	if s == nil || s.db == nil {
		return pms.StateUnknown, fmt.Errorf("bolt metadata service is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return pms.StateUnknown, err
	}

	state := pms.StateUnknown
	err := s.db.View(func(tx *bbolt.Tx) error {
		intentsBucket := tx.Bucket(chunkIntentsBkt)
		if intentsBucket == nil {
			state = pms.StateUnknown
			return nil
		}

		intentBytes := intentsBucket.Get([]byte(txnID))
		if intentBytes == nil {
			state = pms.StateUnknown
			return nil
		}

		var intent rr.ChunkIntentOperation
		if err := json.Unmarshal(intentBytes, &intent); err != nil {
			return fmt.Errorf("unmarshal chunk intent: %w", err)
		}
		state = toMetadataIntentState(intent.State)
		return nil
	})
	if err != nil {
		return pms.StateUnknown, err
	}

	return state, nil
}

func (s *BoltMetadataService) SetIntentState(ctx context.Context, txnID string, state pms.IntentState) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		intentsBucket, err := tx.CreateBucketIfNotExists(chunkIntentsBkt)
		if err != nil {
			return err
		}

		intent := rr.ChunkIntentOperation{
			TxnID:     txnID,
			State:     toRaftIntentState(state),
			Timestamp: time.Now().UnixNano(),
		}

		if existing := intentsBucket.Get([]byte(txnID)); existing != nil {
			if err := json.Unmarshal(existing, &intent); err != nil {
				return fmt.Errorf("unmarshal existing chunk intent: %w", err)
			}
			intent.State = toRaftIntentState(state)
			intent.Timestamp = time.Now().UnixNano()
		}

		intentBytes, err := json.Marshal(intent)
		if err != nil {
			return fmt.Errorf("marshal chunk intent: %w", err)
		}

		return intentsBucket.Put([]byte(txnID), intentBytes)
	})
}

func (s *BoltMetadataService) Create(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	parent, err := s.GetInode(ctx, parentInodeID)
	if err != nil {
		return nil, err
	}
	if parent.Type != pms.TypeDirectory {
		return nil, inmemoryms.ErrNotDir
	}

	if _, err := s.Lookup(ctx, parentInodeID, name); err == nil {
		return nil, inmemoryms.ErrAlreadyExists
	} else if err != nil && err != inmemoryms.ErrNotFound {
		return nil, err
	}

	newID := nextInodeID()
	op := pms.MetadataOperation{
		Type:     pms.OpCreate,
		InodeID:  newID,
		ParentID: parentInodeID,
		Name:     name,
		FileType: pms.TypeFile,
		Mode:     mode,
		UID:      uid,
		GID:      gid,
	}

	if err := s.replicateOp(ctx, op); err != nil {
		return nil, err
	}

	inode, err := s.GetInode(ctx, newID)
	if err != nil {
		if errors.Is(err, inmemoryms.ErrNotFound) {
			return nil, inmemoryms.ErrAlreadyExists
		}
		return nil, err
	}

	return inode, nil
}

func (s *BoltMetadataService) Mkdir(ctx context.Context, parentInodeID string, name string, mode uint32, uid, gid uint32) (*pms.Inode, error) {
	parent, err := s.GetInode(ctx, parentInodeID)
	if err != nil {
		return nil, err
	}
	if parent.Type != pms.TypeDirectory {
		return nil, inmemoryms.ErrNotDir
	}

	if _, err := s.Lookup(ctx, parentInodeID, name); err == nil {
		return nil, inmemoryms.ErrAlreadyExists
	} else if err != nil && err != inmemoryms.ErrNotFound {
		return nil, err
	}

	newID := nextInodeID()
	op := pms.MetadataOperation{
		Type:     pms.OpCreate,
		InodeID:  newID,
		ParentID: parentInodeID,
		Name:     name,
		FileType: pms.TypeDirectory,
		Mode:     mode,
		UID:      uid,
		GID:      gid,
	}

	if err := s.replicateOp(ctx, op); err != nil {
		return nil, err
	}

	return s.GetInode(ctx, newID)
}

func (s *BoltMetadataService) Remove(ctx context.Context, parentInodeID string, name string) error {
	if _, err := s.Lookup(ctx, parentInodeID, name); err != nil {
		return err
	}

	op := pms.MetadataOperation{
		Type:     pms.OpRemove,
		ParentID: parentInodeID,
		Name:     name,
	}
	return s.replicateOp(ctx, op)
}

func (s *BoltMetadataService) Rmdir(ctx context.Context, parentInodeID string, name string) error {
	childID, err := s.Lookup(ctx, parentInodeID, name)
	if err != nil {
		return err
	}
	child, err := s.GetInode(ctx, childID)
	if err != nil {
		return err
	}
	if child.Type != pms.TypeDirectory {
		return inmemoryms.ErrNotDir
	}

	isEmpty, err := s.directoryIsEmpty(ctx, childID)
	if err != nil {
		return err
	}
	if !isEmpty {
		return inmemoryms.ErrNotEmpty
	}

	op := pms.MetadataOperation{
		Type:     pms.OpRemove,
		ParentID: parentInodeID,
		Name:     name,
	}
	return s.replicateOp(ctx, op)
}

func (s *BoltMetadataService) Rename(ctx context.Context, srcParentID, srcName, dstParentID, dstName string) error {
	if _, err := s.Lookup(ctx, srcParentID, srcName); err != nil {
		return err
	}
	dstParent, err := s.GetInode(ctx, dstParentID)
	if err != nil {
		return err
	}
	if dstParent.Type != pms.TypeDirectory {
		return inmemoryms.ErrNotDir
	}

	op := pms.MetadataOperation{
		Type:        pms.OpRename,
		ParentID:    srcParentID,
		Name:        srcName,
		DstParentID: dstParentID,
		DstName:     dstName,
	}
	return s.replicateOp(ctx, op)
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

func loadSuperblock(tx *bbolt.Tx) (*pms.Superblock, error) {
	superblockBkt := tx.Bucket(superblockBucket)
	if superblockBkt == nil {
		return nil, fmt.Errorf("bolt metadata schema is not initialized")
	}

	superblockBytes := superblockBkt.Get(superblockKey)
	if superblockBytes == nil {
		return nil, inmemoryms.ErrNotFound
	}

	var superblock pms.Superblock
	if err := decodeGob(superblockBytes, &superblock); err != nil {
		return nil, fmt.Errorf("decode superblock: %w", err)
	}

	return &superblock, nil
}

func loadStoredInode(inodes *bbolt.Bucket, inodeKey []byte, inodeID string) (*pms.Inode, error) {
	if inodes == nil {
		return nil, fmt.Errorf("bolt metadata schema is not initialized")
	}

	inodeBytes := inodes.Get(inodeKey)
	if inodeBytes == nil {
		return nil, inmemoryms.ErrNotFound
	}

	var inode pms.Inode
	if err := decodeGob(inodeBytes, &inode); err != nil {
		return nil, fmt.Errorf("decode inode %q: %w", inodeID, err)
	}

	return &inode, nil
}

func loadChunkList(chunkMap *bbolt.Bucket, inodeKey []byte) ([]contract.ChunkDescriptor, error) {
	if chunkMap == nil {
		return nil, fmt.Errorf("bolt metadata schema is not initialized")
	}

	cursor := chunkMap.Cursor()
	chunkList := make([]contract.ChunkDescriptor, 0)
	for key, value := cursor.Seek(inodeKey); key != nil && bytes.HasPrefix(key, inodeKey); key, value = cursor.Next() {
		if len(key) != len(inodeKey)+8 {
			return nil, fmt.Errorf("invalid chunk map key length: got %d", len(key))
		}

		index := binary.BigEndian.Uint64(key[len(inodeKey):])
		var chunkDescriptor contract.ChunkDescriptor
		if err := decodeGob(value, &chunkDescriptor); err != nil {
			return nil, fmt.Errorf("decode chunk descriptor at index %d: %w", index, err)
		}

		for uint64(len(chunkList)) <= index {
			chunkList = append(chunkList, contract.ChunkDescriptor{})
		}
		chunkList[index] = chunkDescriptor
	}

	return chunkList, nil
}

func replaceChunkList(chunkMap *bbolt.Bucket, inodeKey []byte, chunkList []contract.ChunkDescriptor) error {
	if err := deleteChunkList(chunkMap, inodeKey); err != nil {
		return err
	}

	for idx, chunkDescriptor := range chunkList {
		valueBytes, err := encodeGob(chunkDescriptor)
		if err != nil {
			return fmt.Errorf("encode chunk descriptor at index %d: %w", idx, err)
		}
		if err := chunkMap.Put(encodeChunkMapKey(inodeKey, uint64(idx)), valueBytes); err != nil {
			return fmt.Errorf("store chunk descriptor at index %d: %w", idx, err)
		}
	}

	return nil
}

func deleteChunkList(chunkMap *bbolt.Bucket, inodeKey []byte) error {
	if chunkMap == nil {
		return fmt.Errorf("bolt metadata schema is not initialized")
	}

	cursor := chunkMap.Cursor()
	for key, _ := cursor.Seek(inodeKey); key != nil && bytes.HasPrefix(key, inodeKey); {
		nextKey, _ := cursor.Next()
		deleteKey := append([]byte(nil), key...)
		if err := chunkMap.Delete(deleteKey); err != nil {
			return fmt.Errorf("delete chunk descriptor for key %x: %w", deleteKey, err)
		}
		key = nextKey
	}

	return nil
}

func encodeChunkMapKey(inodeKey []byte, chunkIndex uint64) []byte {
	key := make([]byte, len(inodeKey)+8)
	copy(key, inodeKey)
	binary.BigEndian.PutUint64(key[len(inodeKey):], chunkIndex)
	return key
}

func nextInodeID() string {
	if inodeIDCounter.Load() == 0 {
		inodeIDCounter.CompareAndSwap(0, uint64(time.Now().UnixNano()))
	}
	return strconv.FormatUint(inodeIDCounter.Add(1), 10)
}

func (s *BoltMetadataService) directoryIsEmpty(ctx context.Context, inodeID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("bolt metadata service is not initialized")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	inodeKey, err := encodeInodeKey(inodeID)
	if err != nil {
		return false, fmt.Errorf("encode inode id %q: %w", inodeID, err)
	}

	empty := true
	err = s.db.View(func(tx *bbolt.Tx) error {
		dentries := tx.Bucket(dentriesBucket)
		inodes := tx.Bucket(inodesBucket)
		if dentries == nil || inodes == nil {
			return fmt.Errorf("bolt metadata schema is not initialized")
		}

		inode, err := loadStoredInode(inodes, inodeKey, inodeID)
		if err != nil {
			return err
		}
		if inode.Type != pms.TypeDirectory {
			return inmemoryms.ErrNotDir
		}

		key, _ := dentries.Cursor().Seek(inodeKey)
		empty = key == nil || !bytes.HasPrefix(key, inodeKey)
		return nil
	})
	if err != nil {
		return false, err
	}

	return empty, nil
}

func decodeInodeID(key []byte) (string, error) {
	if len(key) != 8 {
		return "", fmt.Errorf("invalid inode key length: got %d", len(key))
	}
	return strconv.FormatUint(binary.BigEndian.Uint64(key), 10), nil
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

func toMetadataIntentState(state rr.IntentState) pms.IntentState {
	switch state {
	case rr.StatePrepared:
		return pms.StatePrepared
	case rr.StateCommitted:
		return pms.StateCommitted
	case rr.StateAborted:
		return pms.StateAborted
	default:
		return pms.StateUnknown
	}
}

func toRaftIntentState(state pms.IntentState) rr.IntentState {
	switch state {
	case pms.StatePrepared:
		return rr.StatePrepared
	case pms.StateCommitted:
		return rr.StateCommitted
	case pms.StateAborted:
		return rr.StateAborted
	default:
		return rr.StateUnknown
	}
}

func storableInode(inode pms.Inode) pms.Inode {
	inode.Children = nil
	inode.ChunkList = nil
	return inode
}

// SerializeSnapshot serializes the entire bbolt database to a byte slice.
func (s *BoltMetadataService) SerializeSnapshot() ([]byte, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("bolt metadata service is not initialized")
	}

	var buf bytes.Buffer
	err := s.db.View(func(tx *bbolt.Tx) error {
		_, err := tx.WriteTo(&buf)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to serialize snapshot: %w", err)
	}

	return buf.Bytes(), nil
}

// RestoreSnapshot restores the bbolt database from a given byte slice snapshot.
func (s *BoltMetadataService) RestoreSnapshot(data []byte) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("bolt metadata service is not initialized")
	}

	// 1. Write the snapshot data to a temporary file
	tempFilePath := s.filePath + ".tmp"
	if err := os.WriteFile(tempFilePath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write snapshot to temp file: %w", err)
	}
	defer os.Remove(tempFilePath) // Clean up temp file on failure / success

	// 2. Close the current database
	if err := s.Close(); err != nil {
		return fmt.Errorf("failed to close current db during snapshot restore: %w", err)
	}
	s.db = nil

	// 3. Atomically swap the files (overwrite the old state)
	if err := os.Rename(tempFilePath, s.filePath); err != nil {
		return fmt.Errorf("failed to atomic rename snapshot file: %w", err)
	}

	// 4. Reopen the database
	db, err := bbolt.Open(s.filePath, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return fmt.Errorf("failed to reopen bolt metadata db %q: %w", s.filePath, err)
	}
	s.db = db

	return nil
}
