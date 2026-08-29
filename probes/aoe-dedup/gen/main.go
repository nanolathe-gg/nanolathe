// Command gen writes the aoe-dedup probe's data.
//
// Run from probes/aoe-dedup: `go run ./gen`. Outputs:
//
//	objects3d/ta_probe_blk.3do    navy 2×2-cell block (the target)
//	objects3d/ta_probe_boom.3do   maroon 2×2-cell block (the self-destructor)
//	scripts/TA_PROBE_BLK.COB      minimal Create/Killed scripts
//	scripts/TA_PROBE_BOOM.COB
//	maps/ta_probe_aoe.tnt         flat 2048×2048 px grey map
//	maps/ta_probe_aoe.ota         the mission: 24 targets around one boom
//
// Target layout (cells): a 5×5 grid centred on cell (64,64) with a 3-cell
// pitch (48 px); the centre slot holds TA_PROBE_BOOM, the other 24 hold
// TA_PROBE_BLK. Area damage walks plot cells by increasing Z then X, so
// the row-major order of the targets is also the order in which the
// 20-entry unit memory of [06 §9.3] fills: the 21st..24th distinct units
// in that walk are the last four of the bottom row (labelled 21..24 in the
// README table).
package main

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nanolathe/nanolathe/probes/kit/author"
)

func block(name string, color byte) []byte {
	root := &author.Piece{Name: "base", Selection: 0}
	root.Plate(16, 16, 0, color)
	root.Box(author.Vec{X: -12, Y: 0, Z: -12}, author.Vec{X: 12, Y: 16, Z: 12}, color)
	_ = name
	return author.ThreeDOBytes(root)
}

func main() {
	out := flag.String("out", ".", "probe directory to write into")
	flag.Parse()

	const (
		grey   = 7
		navy   = 4
		maroon = 1
	)
	author.Must(author.WriteFile(filepath.Join(*out, "objects3d", "ta_probe_blk.3do"), block("blk", navy)))
	author.Must(author.WriteFile(filepath.Join(*out, "objects3d", "ta_probe_boom.3do"), block("boom", maroon)))
	cob := author.COBBytes(author.MinimalScripts(), []string{"base"})
	author.Must(author.WriteFile(filepath.Join(*out, "scripts", "TA_PROBE_BLK.COB"), cob))
	author.Must(author.WriteFile(filepath.Join(*out, "scripts", "TA_PROBE_BOOM.COB"), cob))

	t := author.NewTNT(128, 128, 20, author.SolidTile(grey))
	author.Must(author.WriteFile(filepath.Join(*out, "maps", "ta_probe_aoe.tnt"), t.Bytes()))

	var sb strings.Builder
	sb.WriteString(otaHead)
	n := 0
	label := 0
	for row := 0; row < 5; row++ {
		for col := 0; col < 5; col++ {
			cx := 64 + (col-2)*3
			cz := 64 + (row-2)*3
			// Unit positions are map pixels; a 2×2 footprint centred on the
			// cell pair (cx, cx+1) sits at cell boundary (cx+1)·16.
			x := (cx + 1) * 16
			z := (cz + 1) * 16
			name := "TA_PROBE_BLK"
			if row == 2 && col == 2 {
				name = "TA_PROBE_BOOM"
			} else {
				label++
			}
			fmt.Fprintf(&sb, "\t\t\t[unit%d]\n\t\t\t\t{\n\t\t\t\tUnitname=%s;\n\t\t\t\tXPos=%d;\n\t\t\t\tYPos=0;\n\t\t\t\tZPos=%d;\n\t\t\t\tPlayer=1;\n\t\t\t\tHealthPercentage=100;\n\t\t\t\tAngle=0;\n\t\t\t\t}\n", n, name, x, z)
			n++
		}
	}
	// One enemy unit far from the blast keeps the mission running
	// (a mission with no Player=2 unit is won at the first poll, [fmt ota]).
	fmt.Fprintf(&sb, "\t\t\t[unit%d]\n\t\t\t\t{\n\t\t\t\tUnitname=TA_PROBE_BLK;\n\t\t\t\tXPos=1800;\n\t\t\t\tYPos=0;\n\t\t\t\tZPos=1700;\n\t\t\t\tPlayer=2;\n\t\t\t\tHealthPercentage=100;\n\t\t\t\tAngle=0;\n\t\t\t\t}\n", n)
	sb.WriteString(otaTail)
	author.Must(author.WriteFile(filepath.Join(*out, "maps", "ta_probe_aoe.ota"), []byte(sb.String())))
}

const otaHead = `[GlobalHeader]
	{
	missionname=RWU-00-6 AOE dedup probe;
	missiondescription=Probe: self-destruct one block among 24 and read which survive.;
	planet=Green planet;
	missionhint=;
	brief=;
	narration=;
	glamour=;
	lineofsight=0;
	mapping=0;
	tidalstrength=0;
	solarstrength=20;
	lavaworld=0;
	killmul=50;
	timemul=0;
	minwindspeed=100;
	maxwindspeed=3000;
	gravity=112;
	numplayers=2;
	size=4 x 4;
	memory=16 mb;
	SCHEMACOUNT=1;
	[Schema 0]
		{
		Type=Easy;
		aiprofile=;
		SurfaceMetal=3;
		MohoMetal=30;
		HumanMetal=1000;
		ComputerMetal=1000;
		HumanEnergy=1000;
		ComputerEnergy=1000;
		MeteorWeapon=;
		MeteorRadius=0;
		MeteorDensity=0;
		MeteorDuration=0;
		MeteorInterval=0;
		[units]
			{
`

const otaTail = `			}
		[specials]
			{
			[special0]
				{
				specialwhat=StartPos1;
				XPos=1040;
				ZPos=1040;
				}
			}
		}
	}
`
