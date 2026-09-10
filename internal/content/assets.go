package content

// AssetID is a stable, typed presentation asset identity.
type AssetID string

// AssetSequence describes an authored frame sequence and its per-frame
// durations [03 §4.4], [03 §5.5].
type AssetSequence struct {
	ID        AssetID
	Frames    []AssetID
	Durations []uint32
	Loop      bool
}
