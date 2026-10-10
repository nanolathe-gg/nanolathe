package replay_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/nanolathe-gg/nanolathe/formats"
	"github.com/nanolathe-gg/nanolathe/vfs"
)

// portableMap is the authored fixture map: one flat 32×32 tile map with two
// network start positions.
const portableMap = "portable"

// portableFS is an install authored entirely here, the same shape as the
// session's portable checkpoint fixture (internal/session
// checkpoint_portable_fixture_test.go): two weaponless commanders, one per
// side, one returning Create script and one model piece, mounted from an
// in-memory archive through the ordinary HPI writer [fmt hpi]. The real
// compiler, freezer and constructors own everything composed from it.
func portableFS(t *testing.T) vfs.FSOps {
	t.Helper()
	model, err := formats.EncodeThreeDO(&formats.ThreeDO{Root: 0, Objects: []formats.ThreeDOObject{{
		Version: 1, Name: "base", Selection: -1, Parent: -1, FirstChild: -1, NextSibling: -1,
		Vertices: []formats.ThreeDOVertex{{}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	// Authored masks, not an approximation of the retail shapes [fmt gaf].
	frames := make([]formats.GAFWriteFrame, 10)
	for i := range frames {
		frames[i] = formats.GAFWriteFrame{Width: 3, Height: 3, XOffset: 1, YOffset: 1, Pixels: []byte{7, 7, 7, 7, 7, 7, 7, 7, 7}}
	}
	masks, err := formats.EncodeGAF([]formats.GAFWriteEntry{{Name: "vismask", Frames: frames}})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"maps/portable.tnt": string(portableTNT()),
		"maps/portable.ota": `[GlobalHeader]{MinWindSpeed=100;MaxWindSpeed=200;Gravity=112;
[Schema 0]{Type=Network 1;SurfaceMetal=1;
[specials]{[special0]{specialwhat=StartPos1;XPos=128;ZPos=128;}
[special1]{specialwhat=StartPos2;XPos=384;ZPos=320;}}}}`,
		"gamedata/moveinfo.tdf":  `[CLASS0]{Name=portable;FootprintX=1;FootprintZ=1;MaxWaterDepth=10;MaxSlope=10;}`,
		"gamedata/los.tdf":       `[TABLEINFO]{numtables=1;}[TABLE1]{numlines=1;line1=1,0,1;}`,
		"gamedata/sidedata.tdf":  portableSides(),
		"anims/vismasks.gaf":     string(masks),
		"objects3d/portable.3do": string(model),
		"ai/default.txt":         "plan any\n",
	}
	for _, dir := range []string{"weapons", "features", "download", "guis", "unitpics"} {
		files[dir+"/notes.txt"] = "Authored replay fixture; no records in this family.\n"
	}
	for i, name := range []string{"portarm", "portcore"} {
		files["units/"+name+".fbi"] = fmt.Sprintf(`[UNITINFO]{UnitName=%s;Name=Portable Commander;Side=%s;
ObjectName=portable;Category=COMMANDER MOBILE;Commander=1;BMcode=1;CanMove=1;CanStop=1;
MovementClass=portable;MaxVelocity=2;Acceleration=0.25;BrakeRate=0.5;TurnRate=1024;
MaxDamage=1000;SightDistance=160;FootprintX=1;FootprintZ=1;
BuildCostMetal=1;BuildCostEnergy=1;BuildTime=1;}`, name, []string{"ARM", "CORE"}[i])
		files["scripts/"+name+".cob"] = string(portableCOB())
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	archiveFiles := make([]vfs.ArchiveFile, 0, len(paths))
	for _, path := range paths {
		archiveFiles = append(archiveFiles, vfs.ArchiveFile{Path: path, Data: []byte(files[path])})
	}
	var archive bytes.Buffer
	if err := vfs.WriteArchive(&archive, archiveFiles, vfs.ArchiveWriteOptions{}); err != nil {
		t.Fatal(err)
	}
	fs := vfs.New()
	if _, err := fs.MountArchiveReader("portable.hpi", bytes.NewReader(archive.Bytes()), int64(archive.Len()), 10, vfs.ArchiveOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return fs
}

// portableTNT is a canonical TNT: one tile, flat height 20 and no feature
// anchors [fmt tnt].
func portableTNT() []byte {
	const width, height = 32, 32
	const tiles = 0x40
	const attrs = tiles + (width/2)*(height/2)*2
	const graphics = attrs + width*height*4
	const mini = graphics + 1024
	b := make([]byte, mini+8)
	le := binary.LittleEndian
	le.PutUint32(b[0x00:], 0x2000)
	le.PutUint32(b[0x04:], width)
	le.PutUint32(b[0x08:], height)
	le.PutUint32(b[0x0c:], tiles)
	le.PutUint32(b[0x10:], attrs)
	le.PutUint32(b[0x14:], graphics)
	le.PutUint32(b[0x18:], 1)
	le.PutUint32(b[0x20:], mini)
	le.PutUint32(b[0x28:], mini)
	for i := range width * height {
		b[attrs+4*i] = 20
		le.PutUint16(b[attrs+4*i+1:], 0xffff)
	}
	return b
}

// portableCOB is one returning Create script and one matching model piece
// [fmt cob][fmt 3do].
func portableCOB() []byte {
	const code, entries, names, pieces, create, base = 44, 48, 52, 56, 60, 67
	b := make([]byte, 72)
	le := binary.LittleEndian
	le.PutUint32(b[0:], 4)
	le.PutUint32(b[4:], 1)
	le.PutUint32(b[8:], 1)
	le.PutUint32(b[12:], 1)
	le.PutUint32(b[24:], entries)
	le.PutUint32(b[28:], names)
	le.PutUint32(b[32:], pieces)
	le.PutUint32(b[36:], code)
	le.PutUint32(b[40:], create)
	le.PutUint32(b[code:], 0x10065000) // COB return opcode [fmt cob]
	le.PutUint32(b[names:], create)
	le.PutUint32(b[pieces:], base)
	copy(b[create:], "Create\x00")
	copy(b[base:], "base\x00")
	return b
}

// portableSides authors the two sides with the interface anchors side
// compilation requires [02 §6]; their zero rectangles are unused here.
func portableSides() string {
	var b strings.Builder
	for i, side := range []string{"ARM", "CORE"} {
		fmt.Fprintf(&b, "[SIDE%d]{name=%s;commander=%s;font=portable.fnt;", i, side, []string{"portarm", "portcore"}[i])
		for _, name := range strings.Fields("LOGO ENERGYBAR ENERGYNUM ENERGYMAX ENERGY0 METALBAR METALNUM METALMAX METAL0 TOTALUNITS TOTALTIME ENERGYPRODUCED ENERGYCONSUMED METALPRODUCED METALCONSUMED LOGO2 UNITNAME DAMAGEBAR UNITMETALMAKE UNITMETALUSE UNITENERGYMAKE UNITENERGYUSE MISSIONTEXT UNITNAME2 DAMAGEBAR2 NAME DESCRIPTION RELOAD1 RELOAD2 RELOAD3") {
			fmt.Fprintf(&b, "[%s]{}", name)
		}
		b.WriteString("}")
	}
	return b.String()
}
