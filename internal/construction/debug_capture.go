package construction

import "github.com/nanolathe-gg/nanolathe/internal/units"

// admissionHistoryCapacity is a Nanolathe diagnostic retention policy, not a
// retail limit. Indefinite blocked retries must not retain indefinite history.
const admissionHistoryCapacity = 256

// AdmissionFootprint is the half-open cell rectangle actually validated.
// Known is false for definition failures before a placement test is reached.
type AdmissionFootprint struct {
	Known                  bool
	MinX, MinZ, MaxX, MaxZ int32
}

// DebugAdmissionState is a detached recent history in visit order. Total counts
// recorded outcomes since service creation, including evicted records but
// excluding suppressed permanent duplicates. Dropped counts outcomes evicted
// by the diagnostic retention bound.
// AdmissionAdmitted means placement passed, before allocation is attempted.
type DebugAdmissionState struct {
	Capacity int
	Total    uint64
	Dropped  uint64
	Recent   []AdmissionDiagnostic
}

// DebugAdmissionSnapshot observes a stopped owner without advancing construction
// or consuming the history. All returned storage belongs to the caller.
func (s *Service) DebugAdmissionSnapshot() DebugAdmissionState {
	d := DebugAdmissionState{Capacity: admissionHistoryCapacity}
	if s == nil {
		return d
	}
	d.Total = s.admissionsTotal
	d.Dropped = d.Total - uint64(len(s.admissions))
	d.Recent = s.AdmissionDiagnostics()
	return d
}

func (s *Service) appendAdmission(d AdmissionDiagnostic) {
	if s.admissions == nil {
		s.admissions = make([]AdmissionDiagnostic, 0, admissionHistoryCapacity)
	}
	s.admissionsTotal++
	if len(s.admissions) < admissionHistoryCapacity {
		s.admissions = append(s.admissions, d)
		return
	}
	s.admissions[s.admissionsStart] = d
	s.admissionsStart = (s.admissionsStart + 1) % admissionHistoryCapacity
}

func (s *Service) debugBuilderIdentity(u *units.Unit) uint64 {
	if u != nil && s.DebugBuilderIdentity != nil {
		return s.DebugBuilderIdentity(u)
	}
	return 0
}
