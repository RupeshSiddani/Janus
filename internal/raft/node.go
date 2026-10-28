package raft

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	"github.com/neman/janus/internal/lock"
)

type Config struct {
	NodeID    string
	BindAddr  string
	DataDir   string
	Bootstrap bool
	JoinAddr  string
}

type Node struct {
	raft      *raft.Raft
	fsm       *lock.FSM
	raftFSM   *RaftFSM
	transport *raft.NetworkTransport
	storage   *Storage
	cfg       *Config
	stopCh    chan struct{}
	stopOnce  sync.Once
}

func NewNode(cfg *Config) (*Node, error) {
	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	raftFSM := NewRaftFSM()

	raftCfg := raft.DefaultConfig()
	raftCfg.LocalID = raft.ServerID(cfg.NodeID)
	raftCfg.HeartbeatTimeout = 1000 * time.Millisecond
	raftCfg.ElectionTimeout = 1000 * time.Millisecond
	raftCfg.CommitTimeout = 50 * time.Millisecond
	raftCfg.SnapshotThreshold = 8192

	store, err := NewStorage(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("create storage: %w", err)
	}

	addr, err := net.ResolveTCPAddr("tcp", cfg.BindAddr)
	if err != nil {
		return nil, fmt.Errorf("resolve bind addr: %w", err)
	}

	transport, err := raft.NewTCPTransport(cfg.BindAddr, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("create transport: %w", err)
	}

	r, err := raft.NewRaft(raftCfg, raftFSM, store.LogStore, store.StableStore, store.SnapshotStore, transport)
	if err != nil {
		return nil, fmt.Errorf("create raft: %w", err)
	}

	if cfg.Bootstrap {
		hasState, err := raft.HasExistingState(store.LogStore, store.StableStore, store.SnapshotStore)
		if err != nil {
			return nil, fmt.Errorf("check existing state: %w", err)
		}
		if !hasState {
			configuration := raft.Configuration{
				Servers: []raft.Server{
					{ID: raftCfg.LocalID, Address: transport.LocalAddr()},
				},
			}
			r.BootstrapCluster(configuration)
		}
	}

	node := &Node{
		raft:      r,
		fsm:       raftFSM.GetFSM(),
		raftFSM:   raftFSM,
		transport: transport,
		storage:   store,
		cfg:       cfg,
		stopCh:    make(chan struct{}),
	}

	go node.leaseExpiryLoop()

	return node, nil
}

func (n *Node) Apply(cmd lock.Command) (any, error) {
	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("marshal command: %w", err)
	}

	future := n.raft.Apply(data, 5*time.Second)
	if err := future.Error(); err != nil {
		return nil, fmt.Errorf("raft apply: %w", err)
	}

	resp := future.Response()
	if err, ok := resp.(error); ok {
		return nil, err
	}
	return resp, nil
}

func (n *Node) IsLeader() bool         { return n.raft.State() == raft.Leader }
func (n *Node) NodeID() string          { return n.cfg.NodeID }
func (n *Node) State() raft.RaftState   { return n.raft.State() }
func (n *Node) FSM() *lock.FSM          { return n.fsm }

func (n *Node) LeaderAddr() string {
	addr, _ := n.raft.LeaderWithID()
	return string(addr)
}

func (n *Node) ClusterSize() int {
	return len(n.raft.GetConfiguration().Configuration().Servers)
}

func (n *Node) Shutdown() error {
	var err error
	n.stopOnce.Do(func() {
		close(n.stopCh)
		if future := n.raft.Shutdown(); future.Error() != nil {
			err = future.Error()
		}
		n.transport.Close()
		n.storage.Close()
	})
	return err
}

func (n *Node) leaseExpiryLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-n.stopCh:
			return
		case <-ticker.C:
			if !n.IsLeader() {
				continue
			}
			for _, leaseID := range n.fsm.GetExpiredLeases() {
				n.Apply(lock.Command{Type: lock.CmdExpireLease, LeaseID: leaseID})
			}
		}
	}
}
