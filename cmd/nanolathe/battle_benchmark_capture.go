package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/orders"
	"github.com/nanolathe-gg/nanolathe/internal/pool"
	"github.com/nanolathe-gg/nanolathe/internal/session"
	"github.com/nanolathe-gg/nanolathe/internal/sim/numeric"
	"github.com/nanolathe-gg/nanolathe/internal/units"
)

// A capture supplies only placement and command intent. All runtime state is
// freshly created through the ordinary battle allocator and order producers.
// It cannot restore the path, COB, AI, economy or effect histories omitted by
// Ctrl+Shift+F11 (docs/DEBUG_CAPTURE.md).
type captureBenchmarkScene struct {
	CaptureID     string        `json:"capture_id"`
	UnitSHA256    string        `json:"unit_sha256"`
	CapturedTick  uint32        `json:"captured_tick"`
	CapturedUnits int           `json:"captured_units"`
	StagedUnits   int           `json:"staged_units"`
	StartingUnits int           `json:"starting_units"`
	OrdersIssued  int           `json:"orders_issued"`
	OrdersOmitted int           `json:"orders_omitted"`
	CameraX       int32         `json:"camera_x"`
	CameraZ       int32         `json:"camera_z"`
	SourceWidth   int32         `json:"source_width"`
	SourceHeight  int32         `json:"source_height"`
	Owners        [4]int        `json:"owners"`
	Factories     []*units.Unit `json:"-"`
}

type captureBenchmarkManifest struct {
	CaptureID string `json:"CaptureID"`
	Complete  bool   `json:"Complete"`
	Metadata  struct {
		Map               string `json:"map"`
		AuthoritativeTick uint32 `json:"authoritative_tick"`
	} `json:"Metadata"`
}

type captureBenchmarkClient struct {
	Camera struct {
		X, Z         int32
		ViewW, ViewH int32
	} `json:"camera"`
}

type captureBenchmarkOrder struct {
	DescriptorID uint8
	Target       uint32
	GoalX        int64
	GoalY        int64
	GoalZ        int64
	BuildProduct string
	BuildCount   int
}

type captureBenchmarkUnit struct {
	Unit struct {
		Slot          int
		DefinitionKey string
		Owner         uint8
		Alive         bool
		X, Y, Z       int64
		Health        int32
		Move          struct {
			Mode    uint8
			Heading uint16
		}
	}
	Orders struct {
		Queue struct {
			Primary []captureBenchmarkOrder
		}
	}
}

func readCaptureBenchmarkJSON(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(target)
}

func stageCaptureBenchmark(opts Options, s *session.Session) (*captureBenchmarkScene, error) {
	base := opts.BenchmarkCapture
	var manifest captureBenchmarkManifest
	if err := readCaptureBenchmarkJSON(filepath.Join(base, "manifest.json"), &manifest); err != nil {
		return nil, fmt.Errorf("nanolathe: read benchmark capture manifest: %w", err)
	}
	if !strings.EqualFold(manifest.Metadata.Map, opts.Map) {
		return nil, fmt.Errorf("nanolathe: benchmark capture map mismatch: logical path %s, providers searched [manifest.json], expected %s", opts.Map, manifest.Metadata.Map)
	}
	if !manifest.Complete {
		return nil, fmt.Errorf("nanolathe: benchmark capture incomplete: logical path %s, providers searched [manifest.json], expected a complete diagnostic bundle", base)
	}
	var client captureBenchmarkClient
	if err := readCaptureBenchmarkJSON(filepath.Join(base, "client.json"), &client); err != nil {
		return nil, fmt.Errorf("nanolathe: read benchmark capture camera: %w", err)
	}
	if client.Camera.ViewW <= 0 || client.Camera.ViewH <= 0 {
		return nil, fmt.Errorf("nanolathe: benchmark capture camera missing: logical path %s, providers searched [client.json], expected positive viewport dimensions", base)
	}
	var shotW, shotH int32
	if _, err := fmt.Sscanf(opts.ShotSize, "%dx%d", &shotW, &shotH); err != nil || shotW != client.Camera.ViewW || shotH != client.Camera.ViewH {
		return nil, fmt.Errorf("nanolathe: benchmark capture viewport mismatch: logical path %s, providers searched [client.json], expected --shot-size=%dx%d", opts.ShotSize, client.Camera.ViewW, client.Camera.ViewH)
	}
	f, err := os.Open(filepath.Join(base, "units.jsonl"))
	if err != nil {
		return nil, fmt.Errorf("nanolathe: read benchmark capture units: %w", err)
	}
	defer f.Close()
	hash := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(f, hash))
	var rows []captureBenchmarkUnit
	for {
		var row captureBenchmarkUnit
		err := decoder.Decode(&row)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("nanolathe: decode benchmark capture unit: %w", err)
		}
		if row.Unit.Slot <= 0 || row.Unit.DefinitionKey == "" || !row.Unit.Alive {
			return nil, fmt.Errorf("nanolathe: invalid benchmark capture unit: logical path %s, providers searched [units.jsonl], expected live units with slot and definition", base)
		}
		rows = append(rows, row)
	}
	scene := &captureBenchmarkScene{
		CaptureID: manifest.CaptureID, UnitSHA256: hex.EncodeToString(hash.Sum(nil)),
		CapturedTick: manifest.Metadata.AuthoritativeTick, CapturedUnits: len(rows),
		CameraX: client.Camera.X, CameraZ: client.Camera.Z,
		SourceWidth: client.Camera.ViewW, SourceHeight: client.Camera.ViewH,
	}
	for _, u := range s.Units.Iter() {
		if u != nil && u.Alive {
			scene.StartingUnits++
		}
	}
	bySlot := make(map[int]*units.Unit, len(rows))
	for _, row := range rows {
		if int(row.Unit.Owner) >= s.Skirmish.NumPlayers {
			return nil, fmt.Errorf("nanolathe: benchmark capture owner out of range: logical path %d, providers searched [survival setup], expected owner below %d", row.Unit.Owner, s.Skirmish.NumPlayers)
		}
		def, ok := s.Catalog.Unit(row.Unit.DefinitionKey)
		if !ok {
			return nil, fmt.Errorf("nanolathe: benchmark capture unit missing: logical path %s, providers searched [catalog], expected an authored unit definition", row.Unit.DefinitionKey)
		}
		h, err := s.Units.CreateWithMoverMode(def, row.Unit.Owner, numeric.Fixed(row.Unit.X), numeric.Fixed(row.Unit.Y), numeric.Fixed(row.Unit.Z), row.Unit.Move.Mode)
		if err != nil {
			return nil, fmt.Errorf("nanolathe: benchmark create %s at slot %d: %w", row.Unit.DefinitionKey, row.Unit.Slot, err)
		}
		u := s.Units.Unit(h)
		u.Move.Heading = row.Unit.Move.Heading
		s.Movement.EnsureUnit(u)
		s.BindStagedOrderQueue(u)
		scene.StagedUnits++
		if row.Unit.Health > 0 && row.Unit.Health <= u.MaxHealth {
			u.Health = row.Unit.Health
		}
		if _, exists := bySlot[row.Unit.Slot]; exists {
			return nil, fmt.Errorf("nanolathe: duplicate benchmark capture slot: logical path %d, providers searched [units.jsonl], expected unique slots", row.Unit.Slot)
		}
		bySlot[row.Unit.Slot] = u
		if int(row.Unit.Owner) < len(scene.Owners) {
			scene.Owners[row.Unit.Owner]++
		}
	}
	for _, row := range rows {
		u := bySlot[row.Unit.Slot]
		if u == nil {
			continue
		}
		for _, source := range row.Orders.Queue.Primary {
			id := orders.ID(source.DescriptorID)
			name := orders.DescriptorFor(id).Name
			if name == "BuildingBuild" && opts.BenchmarkFactories && source.BuildProduct != "" {
				count := source.BuildCount
				if count < 1 {
					count = 1
				}
				if err := construction.QueueFactoryBuild(u, source.BuildProduct, count, s.Catalog); err == nil {
					scene.OrdersIssued++
					scene.Factories = append(scene.Factories, u)
					continue
				}
			}
			if !captureBenchmarkSimpleOrder(name) {
				scene.OrdersOmitted++
				continue
			}
			var target pool.Handle
			if source.Target != 0 {
				if peer := bySlot[int(source.Target)]; peer != nil {
					target = peer.Handle
				}
			}
			n := orders.NewNodeForOrder(id, target, numeric.Fixed(source.GoalX), numeric.Fixed(source.GoalY), numeric.Fixed(source.GoalZ), s.Clock.GlobalTick, u.Handle, true)
			orders.QueueForUnit(u).Push(id, n)
			scene.OrdersIssued++
		}
	}
	fmt.Printf("capture scene=%s captured_units=%d starting_units=%d orders=%d omitted=%d camera=%d,%d\n", scene.CaptureID, scene.CapturedUnits, scene.StartingUnits, scene.OrdersIssued, scene.OrdersOmitted, scene.CameraX, scene.CameraZ)
	return scene, nil
}

func captureBenchmarkSimpleOrder(name string) bool {
	switch name {
	case "Patrol", "QPatrol", "Move_Ground", "QMove", "VTOL_Patrol", "VTOL_Move", "Attack_Chase", "AirStrike", "AirToAir", "AirToGround", "AirToGroundHover", "Guard_NoMove", "Follow_Ground":
		return true
	}
	return false
}
