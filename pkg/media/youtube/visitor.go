package youtube

import (
	"encoding/json"
	"net/url"
	"sync"
)

type Visitor struct {
	keep func(string)

	mu sync.Mutex
	id string
}

func NewVisitor(id string, keep func(string)) *Visitor {
	return &Visitor{id: id, keep: keep}
}

func (v *Visitor) ID() string {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.id
}

func (v *Visitor) learn(raw []byte) {
	var r struct {
		Context struct {
			Visitor string `json:"visitorData"`
		} `json:"responseContext"`
	}
	if json.Unmarshal(raw, &r) != nil || r.Context.Visitor == "" {
		return
	}
	id, err := url.PathUnescape(r.Context.Visitor)
	if err != nil {
		return
	}
	v.mu.Lock()
	if v.id != "" {
		v.mu.Unlock()
		return
	}
	v.id = id
	v.mu.Unlock()
	if v.keep != nil {
		v.keep(id)
	}
}
