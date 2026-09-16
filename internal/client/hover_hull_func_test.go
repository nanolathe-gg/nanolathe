package client

import compiledmodel "github.com/nanolathe-gg/nanolathe/internal/model"

// UnitHullModelFunc adapts a plain lookup to UnitHullModels. No shipped caller
// builds one — every production source is a real model cache — so it lives here
// for the picker fixtures of this package.
type UnitHullModelFunc func(name string) *compiledmodel.Model

// HullModel calls f, or returns nil when f is nil.
func (f UnitHullModelFunc) HullModel(name string) *compiledmodel.Model {
	if f == nil {
		return nil
	}
	return f(name)
}
