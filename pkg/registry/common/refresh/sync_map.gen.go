// Code generated by "go-syncmap -output sync_map.gen.go -type cancelsMap<string,context.CancelFunc>"; DO NOT EDIT.

package refresh

import (
	"context"
	"sync"
)

func _() {
	// An "cannot convert cancelsMap literal (type cancelsMap) to type sync.Map" compiler error signifies that the base type have changed.
	// Re-run the go-syncmap command to generate them again.
	_ = (sync.Map)(cancelsMap{})
}

var _nil_cancelsMap_context_CancelFunc_value = func() (val context.CancelFunc) { return }()

func (m *cancelsMap) Store(key string, value context.CancelFunc) {
	(*sync.Map)(m).Store(key, value)
}

func (m *cancelsMap) LoadOrStore(key string, value context.CancelFunc) (context.CancelFunc, bool) {
	actual, loaded := (*sync.Map)(m).LoadOrStore(key, value)
	if actual == nil {
		return _nil_cancelsMap_context_CancelFunc_value, loaded
	}
	return actual.(context.CancelFunc), loaded
}

func (m *cancelsMap) Load(key string) (context.CancelFunc, bool) {
	value, ok := (*sync.Map)(m).Load(key)
	if value == nil {
		return _nil_cancelsMap_context_CancelFunc_value, ok
	}
	return value.(context.CancelFunc), ok
}

func (m *cancelsMap) Delete(key string) {
	(*sync.Map)(m).Delete(key)
}

func (m *cancelsMap) Range(f func(key string, value context.CancelFunc) bool) {
	(*sync.Map)(m).Range(func(key, value interface{}) bool {
		return f(key.(string), value.(context.CancelFunc))
	})
}
