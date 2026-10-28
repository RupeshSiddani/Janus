package raft

import (
	"encoding/json"
	"io"

	"github.com/hashicorp/raft"
	"github.com/neman/janus/internal/lock"
)

type RaftFSM struct {
	fsm *lock.FSM
}

func NewRaftFSM() *RaftFSM {
	return &RaftFSM{fsm: lock.NewFSM()}
}

func (rf *RaftFSM) Apply(log *raft.Log) any {
	var cmd lock.Command
	if err := json.Unmarshal(log.Data, &cmd); err != nil {
		return err
	}

	result, err := rf.fsm.Apply(cmd)
	if err != nil {
		return err
	}
	return result
}

func (rf *RaftFSM) Snapshot() (raft.FSMSnapshot, error) {
	snap := rf.fsm.CreateSnapshot()
	return &fsmSnapshot{data: snap}, nil
}

func (rf *RaftFSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	var snap lock.Snapshot
	if err := json.NewDecoder(rc).Decode(&snap); err != nil {
		return err
	}
	rf.fsm.RestoreSnapshot(&snap)
	return nil
}

func (rf *RaftFSM) GetFSM() *lock.FSM {
	return rf.fsm
}

type fsmSnapshot struct {
	data *lock.Snapshot
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	if err := json.NewEncoder(sink).Encode(s.data); err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}
