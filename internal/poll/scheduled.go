package poll

import (
	"context"
	"sync/atomic"

	pluginv1 "github.com/Silo-Server/silo-plugin-sdk/pkg/pluginproto/silo/plugin/v1"

	"github.com/RXWatcher/silo-plugin-stream-dashboard/internal/store"
)

type Server struct {
	pluginv1.UnimplementedScheduledTaskServer
	store  atomic.Pointer[store.Store]
	policy atomic.Pointer[store.RetentionPolicy]
}

func New() *Server { return &Server{} }

func (s *Server) Set(st *store.Store, policy store.RetentionPolicy) {
	s.store.Store(st)
	s.policy.Store(&policy)
}

func (s *Server) Run(ctx context.Context, _ *pluginv1.RunScheduledTaskRequest) (*pluginv1.RunScheduledTaskResponse, error) {
	st := s.store.Load()
	policy := s.policy.Load()
	if st == nil || policy == nil {
		return &pluginv1.RunScheduledTaskResponse{}, nil
	}
	_, _, err := st.SyncPlaybackHistory(ctx, *policy)
	if err != nil {
		return nil, err
	}
	return &pluginv1.RunScheduledTaskResponse{}, nil
}
