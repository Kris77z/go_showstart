package monitor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/staparx/go_showstart/log"
	"go.uber.org/zap"
)

type StateManager struct {
	seenPath  string
	timedPath string
	mux       sync.RWMutex
	seen      map[string]struct{}
	timed     map[string]struct{}
}

func NewStateManager(dir string) (*StateManager, error) {
	if dir == "" {
		dir = "monitor_state"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("创建状态目录失败: %w", err)
	}

	mgr := &StateManager{
		seenPath:  filepath.Join(dir, "seen_events.json"),
		timedPath: filepath.Join(dir, "timed_purchase.json"),
		seen:      map[string]struct{}{},
		timed:     map[string]struct{}{},
	}

	if err := mgr.load(); err != nil {
		return nil, err
	}

	return mgr, nil
}

func (s *StateManager) HasSeen(id string) bool {
	s.mux.RLock()
	defer s.mux.RUnlock()
	_, ok := s.seen[id]
	return ok
}

func (s *StateManager) MarkSeen(id string) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.seen[id] = struct{}{}
	s.persist()
}

func (s *StateManager) HasTimed(id string) bool {
	s.mux.RLock()
	defer s.mux.RUnlock()
	_, ok := s.timed[id]
	return ok
}

func (s *StateManager) MarkTimed(id string) {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.timed[id] = struct{}{}
	s.persist()
}

func (s *StateManager) load() error {
	if err := s.readFile(s.seenPath, &s.seen); err != nil {
		return err
	}
	if err := s.readFile(s.timedPath, &s.timed); err != nil {
		return err
	}
	return nil
}

func (s *StateManager) persist() {
	if err := s.writeFile(s.seenPath, s.seen); err != nil {
		log.Logger.Error("写入 seen 状态失败", zap.Error(err))
	}
	if err := s.writeFile(s.timedPath, s.timed); err != nil {
		log.Logger.Error("写入 timed 状态失败", zap.Error(err))
	}
}

func (s *StateManager) readFile(path string, target *map[string]struct{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			*target = map[string]struct{}{}
			return nil
		}
		return err
	}

	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}

	res := make(map[string]struct{}, len(arr))
	for _, id := range arr {
		res[id] = struct{}{}
	}
	*target = res
	return nil
}

func (s *StateManager) writeFile(path string, data map[string]struct{}) error {
	arr := make([]string, 0, len(data))
	for id := range data {
		arr = append(arr, id)
	}

	bytes, err := json.MarshalIndent(arr, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, bytes, 0o644)
}

