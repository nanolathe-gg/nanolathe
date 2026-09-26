package headless

import (
	"fmt"
	"os"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/aikit"
	"github.com/nanolathe-gg/nanolathe/internal/construction"
	"github.com/nanolathe-gg/nanolathe/internal/content"
)

func TestArenaDumpTable(t *testing.T) {
	if os.Getenv("AIKIT_DUMP") == "" {
		t.Skip("set AIKIT_DUMP")
	}
	fs, err := mountContentRoots("", nil)
	if err != nil {
		t.Skip(err)
	}
	defer fs.Close()
	view, profile, _ := contentProfileView(fs, "")
	cat, err := content.CompileWithOptions(view, content.Options{Limits: content.LimitsFromProfile(profile.Limits)})
	if err != nil {
		t.Fatal(err)
	}
	table := aikit.BuildTable(cat, &construction.ModernRules{})
	out, _ := os.Create(os.Getenv("AIKIT_DUMP"))
	defer out.Close()
	for _, key := range []string{"armcom", "corcom", "armck", "armcv", "corck", "corcv", "armlab", "armvp", "corlab", "corvp"} {
		u := table.Lookup(key)
		if u == nil {
			continue
		}
		fmt.Fprintf(out, "== %s side=%s role=%b depth=%d M=%d E=%d BT=%d HP=%d DPS=%d range=%d speed=%d bp=%d\n", u.Key, u.Side, u.Role, u.Depth, u.Metal, u.Energy, u.BuildTime, u.HP, u.DPS, u.Range, u.Speed, u.BuildPower)
		for _, p := range u.Builds {
			d := p.Def
			fmt.Fprintf(out, "   %-10s role=%-26b depth=%d M=%-5d E=%-6d BT=%-6d HP=%-5d DPS=%-4d AA=%-4d rng=%-4d spd=%-3d emake=%v euse=%v mmake=%v xm=%v wind=%v foot=%dx%d value=%d\n",
				p.Key, p.Role, p.Depth, p.Metal, p.Energy, p.BuildTime, p.HP, p.DPS, p.AirDPS, p.Range, p.Speed, d.EnergyMake, d.EnergyUse, d.MetalMake, d.ExtractsMetal, d.WindGenerator, p.FootX, p.FootZ, p.Value)
		}
	}
}

func TestArenaDumpWeapons(t *testing.T) {
	if os.Getenv("AIKIT_DUMPW") == "" {
		t.Skip("set AIKIT_DUMPW")
	}
	fs, err := mountContentRoots("", nil)
	if err != nil {
		t.Skip(err)
	}
	defer fs.Close()
	view, profile, _ := contentProfileView(fs, "")
	cat, err := content.CompileWithOptions(view, content.Options{Limits: content.LimitsFromProfile(profile.Limits)})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"armjeth", "armrl", "armpw", "armcom", "armsam", "armfig", "armthund", "corak", "armflash", "armstump", "armfav"} {
		def, ok := cat.Unit(key)
		if !ok {
			continue
		}
		for _, w := range []*content.WeaponDef{def.Weapon1Def, def.Weapon2Def, def.Weapon3Def} {
			if w == nil || content.IsWeaponInactive(w) {
				continue
			}
			fmt.Printf("%s %s toair=%v cmdfire=%v dmgdef=%d dmg=%v reload=%d range=%d ballistic=%v\n", key, w.Name, w.ToAirWeapon, w.CommandFire, w.DamageDefault, w.Damage, w.ReloadTime, w.Range, w.Ballistic)
		}
		fmt.Printf("  %s category=%q\n", key, def.Category)
	}
}

func TestArenaDumpMaps(t *testing.T) {
	if os.Getenv("AIKIT_MAPS") == "" {
		t.Skip("set AIKIT_MAPS")
	}
	fs, err := mountContentRoots("", nil)
	if err != nil {
		t.Skip(err)
	}
	defer fs.Close()
	view, profile, _ := contentProfileView(fs, "")
	cat, err := content.CompileWithOptions(view, content.Options{Limits: content.LimitsFromProfile(profile.Limits)})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.Create(os.Getenv("AIKIT_MAPS"))
	defer out.Close()
	keys := make([]string, 0, len(cat.Maps))
	for k := range cat.Maps {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		h := cat.Maps[k]
		starts := 0
		for _, s := range h.Schemas {
			if s.StartPosCount > starts {
				starts = s.StartPosCount
			}
		}
		fmt.Fprintf(out, "%-28s size=%-8s players=%-6s starts=%d planet=%-12s schemas=%d\n", h.Name, h.Size, h.NumPlayers, starts, h.Planet, len(h.Schemas))
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
