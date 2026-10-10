package checkpoint

import (
	"bufio"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
)

// SchemaVersion and the owner IDs are fixed by DESIGN_MULTIPLAYER §16.3.6.
const SchemaVersion uint16 = 2
const OwnerCount = 13

type Owner uint16

const (
	OwnerRuntime           Owner = 1
	OwnerUnits             Owner = 2
	OwnerOrders            Owner = 3
	OwnerScripts           Owner = 4
	OwnerWorld             Owner = 5
	OwnerVisibility        Owner = 6
	OwnerMovement          Owner = 7
	OwnerPaths             Owner = 8
	OwnerEconomy           Owner = 9
	OwnerConstruction      Owner = 10
	OwnerCombat            Owner = 11
	OwnerEffects           Owner = 12
	OwnerComputersScenario Owner = 13
)

type Digest [32]byte

// Definition contains the admitted M2 manifest family, record ordinal and key.
// The content owner validates admission before handing the value to an encoder.
type Definition struct {
	Family  uint8
	Ordinal uint32
	Key     string
}

type Allocation struct {
	Handle uint32
	Serial uint64
}

type ObjectID uint32

type Identity struct {
	Content, Config Digest
}

type Digests struct {
	Full   Digest
	Owners [OwnerCount]Digest
}

// Capture tees one ordered encoding to the full hash, current owner hash and
// optional output. Its output is usable only after Finish succeeds.
type Capture struct {
	identity   Identity
	out        io.Writer
	full       hash.Hash
	section    hash.Hash
	hashBuffer *bufio.Writer
	current    *Encoder
	digests    Digests
	next       Owner
	err        error
	finished   bool
}

// NewCapture writes the canonical header. A nil out computes hashes only.
// On failure, any partial bytes written to out must be discarded.
func NewCapture(identity Identity, out io.Writer) (*Capture, error) {
	c := &Capture{identity: identity, out: out, full: sha256.New(), next: OwnerRuntime}
	w := io.Writer(c.full)
	if out != nil {
		w = io.MultiWriter(c.full, out)
	}
	e := NewEncoder(w)
	e.capture = c
	e.Field("header")
	e.write([]byte("NLCPSTAT"))
	e.U16(SchemaVersion)
	e.write(identity.Content[:])
	e.write(identity.Config[:])
	e.U16(OwnerCount)
	if e.Err() != nil {
		return nil, e.Err()
	}
	return c, nil
}

// Section begins the next owner, closing the preceding encoder. An absent
// owner still has a digest, but its returned encoder rejects all payload writes.
func (c *Capture) Section(owner Owner, present bool) (*Encoder, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.finished {
		return nil, c.fail(owner, "sections", errors.New("capture is finished"))
	}
	if owner < OwnerRuntime || owner > OwnerComputersScenario || owner != c.next {
		return nil, c.fail(owner, "sections", fmt.Errorf("section out of order: got %d, expected %d", owner, c.next))
	}
	c.closeSection()
	if c.err != nil {
		return nil, c.err
	}
	c.section = sha256.New()
	// Owner domains omit the section count and include only their own exact
	// owner/presence/payload bytes after this identity prefix.
	domain := NewEncoder(c.section)
	domain.write([]byte("NLCPSECT"))
	domain.U16(SchemaVersion)
	domain.write(c.identity.Content[:])
	domain.write(c.identity.Config[:])
	// One copy per primitive; fan out to both hashes only in complete chunks.
	// The caller's sink stays outside this buffer for exact failure attribution.
	hashes := io.MultiWriter(c.full, c.section)
	if c.hashBuffer == nil {
		c.hashBuffer = bufio.NewWriterSize(hashes, 4096)
	} else {
		c.hashBuffer.Reset(hashes)
	}
	w := io.Writer(c.hashBuffer)
	if c.out != nil {
		w = io.MultiWriter(c.hashBuffer, c.out)
	}
	e := NewEncoder(w)
	e.capture = c
	e.owner = owner
	e.Field("section")
	c.current = e
	e.U16(uint16(owner))
	e.Bool(present)
	e.absent = !present
	if e.Err() != nil {
		return nil, e.Err()
	}
	c.next++
	return e, nil
}

// Finish closes the final encoder and returns hashes only after every owner
// has appeared exactly once in schema order. It may be called only once.
func (c *Capture) Finish() (Digests, error) {
	if c.err != nil {
		return Digests{}, c.err
	}
	if c.finished {
		return Digests{}, c.fail(0, "sections", errors.New("capture is finished"))
	}
	c.finished = true
	c.closeSection()
	if c.err != nil {
		return Digests{}, c.err
	}
	if c.next != OwnerComputersScenario+1 {
		return Digests{}, c.fail(c.next, "sections", errors.New("missing section"))
	}
	copy(c.digests.Full[:], c.full.Sum(nil))
	return c.digests, nil
}

func (c *Capture) closeSection() {
	if c.current != nil {
		c.current.closed = true
		if err := c.hashBuffer.Flush(); err != nil {
			c.fail(c.current.owner, "digest", err)
			return
		}
		copy(c.digests.Owners[c.current.owner-1][:], c.section.Sum(nil))
	}
}

func (c *Capture) fail(owner Owner, path string, err error) error {
	if c.err == nil {
		c.err = contextualError(owner, path, err)
	}
	return c.err
}

func ownerName(owner Owner) string {
	names := [...]string{"stream", "runtime", "units", "orders", "scripts", "world", "visibility", "movement", "paths", "economy", "construction", "combat", "effects", "computers-scenario"}
	if int(owner) < len(names) {
		return names[owner]
	}
	return fmt.Sprintf("%d", owner)
}
