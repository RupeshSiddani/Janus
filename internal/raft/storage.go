package raft

import (
	"os"
	"path/filepath"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

type Storage struct {
	LogStore      raft.LogStore
	StableStore   raft.StableStore
	SnapshotStore raft.SnapshotStore
}

func NewStorage(dataDir string) (*Storage, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dataDir, "raft.db")
	boltDB, err := raftboltdb.New(raftboltdb.Options{Path: dbPath})
	if err != nil {
		return nil, err
	}

	snapDir := filepath.Join(dataDir, "snapshots")
	snapStore, err := raft.NewFileSnapshotStore(snapDir, 3, os.Stderr)
	if err != nil {
		boltDB.Close()
		return nil, err
	}

	return &Storage{
		LogStore:      boltDB,
		StableStore:   boltDB,
		SnapshotStore: snapStore,
	}, nil
}

func (s *Storage) Close() error {
	if closer, ok := s.LogStore.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
