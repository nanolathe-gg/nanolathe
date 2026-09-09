package features

// RuntimeView is the presentation-safe state of an attached feature runtime
// record [05 R-FEAT-01 §2][05 R-FEAT-01 §3][05 R-FEAT-01 §10]. It deliberately does not expose the
// convenience Instance retained for a resting sprite anchor.
type RuntimeView struct {
	Live          bool
	ShadowEnabled bool
}

// RuntimeView reports only state written by arena attachment and release. It
// does not decide liveness from an Instance pointer or from a cursor name:
// both exist outside the retail attached-record predicate [05 R-FEAT-01 §2].
func (i *Instance) RuntimeView() RuntimeView {
	if i == nil || !i.runtimeLive {
		return RuntimeView{}
	}
	return RuntimeView{Live: true, ShadowEnabled: i.runtimeShadowEnabled}
}
