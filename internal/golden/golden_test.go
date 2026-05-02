package golden_test

import (
	"bytes"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/bholten/tools/idlc-go/internal/corpus"
	"github.com/bholten/tools/idlc-go/internal/emit/cpp"
	"github.com/bholten/tools/idlc-go/internal/parser"
	"github.com/bholten/tools/idlc-go/internal/sema"
)

// -update writes any mismatches back to testdata/autogen/. Off by default.
var update = flag.Bool("update", false, "write generated output to testdata/autogen/ instead of diffing")

// repoRoot resolves the workspace root from this test file's location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
}

func TestGoldenChatMessage(t *testing.T) {
	runGolden(t, "ChatMessage", filepath.Join("server", "chat"))
}

func TestGoldenPendingMessageList(t *testing.T) {
	runGolden(t, "PendingMessageList", filepath.Join("server", "chat"))
}

func TestGoldenManagedObject(t *testing.T) {
	runGolden(t, "ManagedObject", filepath.Join("engine", "core"))
}

func TestGoldenChatRoom(t *testing.T) {
	runGolden(t, "ChatRoom", filepath.Join("server", "chat", "room"))
}

func TestGoldenPersistentMessage(t *testing.T) {
	runGolden(t, "PersistentMessage", filepath.Join("server", "chat"))
}

func TestGoldenZone(t *testing.T) {
	runGolden(t, "Zone", filepath.Join("server", "zone"))
}

func TestGoldenChatManager(t *testing.T) {
	runGolden(t, "ChatManager", filepath.Join("server", "chat"))
}

func TestGoldenTreeEntry(t *testing.T) {
	runGolden(t, "TreeEntry", filepath.Join("server", "zone"))
}

func TestGoldenLambdaObserver(t *testing.T) {
	runGolden(t, "LambdaObserver", filepath.Join("server", "utils"))
}

func TestGoldenZoneClientSession(t *testing.T) {
	runGolden(t, "ZoneClientSession", filepath.Join("server", "zone"))
}

func TestGoldenZoneProcessServer(t *testing.T) {
	runGolden(t, "ZoneProcessServer", filepath.Join("server", "zone"))
}

func TestGoldenGroundZone(t *testing.T) {
	runGolden(t, "GroundZone", filepath.Join("server", "zone"))
}

func TestGoldenSpaceZone(t *testing.T) {
	runGolden(t, "SpaceZone", filepath.Join("server", "zone"))
}

func TestGoldenManagedService(t *testing.T) {
	runGolden(t, "ManagedService", filepath.Join("engine", "core"))
}

func TestGoldenManagedVector(t *testing.T) {
	runGolden(t, "ManagedVector", filepath.Join("engine", "core", "util"))
}

func TestGoldenFacade(t *testing.T) {
	runGolden(t, "Facade", filepath.Join("engine", "util"))
}

func TestGoldenObservable(t *testing.T) {
	runGolden(t, "Observable", filepath.Join("engine", "util"))
}

func TestGoldenObserver(t *testing.T) {
	runGolden(t, "Observer", filepath.Join("engine", "util"))
}

func TestGoldenTestIDLClass(t *testing.T) {
	runGolden(t, "TestIDLClass", filepath.Join("testsuite3", "tests"))
}

func TestGoldenTestNoOrbClass(t *testing.T) {
	runGolden(t, "TestNoOrbClass", filepath.Join("testsuite3", "tests"))
}

func runGolden(t *testing.T, className, pkgDir string) {
	t.Helper()
	corpus.RequireOrSkip(t)
	root := repoRoot(t)
	idlPath := filepath.Join(root, "testdata", "idl", className+".idl")
	wantHeader := filepath.Join(root, "testdata", "autogen", pkgDir, className+".h")
	wantSource := filepath.Join(root, "testdata", "autogen", pkgDir, className+".cpp")

	src, err := os.ReadFile(idlPath)

	if err != nil {
		t.Fatal(err)
	}

	f, err := parser.Parse(className+".idl", src)

	if err != nil {
		t.Fatal(err)
	}

	m, err := sema.Resolve(f)

	if err != nil {
		t.Fatal(err)
	}

	reg := sema.NewRegistry()

	if err := reg.LoadFromDir(filepath.Join(root, "testdata", "idl")); err != nil {
		t.Fatal(err)
	}

	// External IDL classes referenced by goldens but not present in the
	// trimmed test corpus. The JAR producing the goldens saw the full
	// Core3 source tree; we register the known managed-object classes
	// the test goldens rely on.
	for _, qname := range []string{
		"server.zone.objects.creature.CreatureObject",
		"server.zone.objects.scene.SceneObject",
		"server.zone.objects.area.ActiveArea",
		"server.zone.objects.tangible.TangibleObject",
		"server.zone.objects.pathfinding.NavArea",
		"server.zone.managers.planet.PlanetManager",
		"server.zone.managers.space.SpaceManager",
		"server.zone.managers.creature.CreatureManager",
		"server.zone.managers.gcw.GCWManager",
		"server.zone.managers.player.PlayerManager",
		"server.zone.objects.player.PlayerObject",
		"server.zone.objects.waypoint.WaypointObject",
		// ZoneProcessServer: managed-IDL classes whose impl-side fields
		// wrap as `ManagedReference<X* >` (and adapter cases insert by
		// object id). All four have a `XPOD` companion in their forward-
		// decl block.
		"server.zone.managers.objectcontroller.ObjectController",
		"server.zone.managers.minigames.FishingManager",
		"server.zone.managers.minigames.GamblingManager",
		"server.zone.managers.minigames.ForageManager",
		// GroundZone: CityRegion is `include`d in the IDL but the JAR
		// still wraps it as `ManagedReference<CityRegion* >` inside
		// generic args, so it has to be classified as a managed IDL
		// class regardless of import-vs-include keyword.
		"server.zone.objects.region.CityRegion",
	} {
		reg.Add(qname)
	}

	// Forward-decl-only IDL classes (no POD generated by the JAR).
	for _, qname := range []string{
		"server.zone.ActiveAreaQuadTree",
		"server.zone.ActiveAreaOctree",
		"server.zone.packets.chat.ChatInstantMessageToCharacter",
		// ZoneProcessServer: forward-decl-only IDL classes — wrap as
		// `Reference<X* >` (no `ManagedReference`, no POD line).
		"server.zone.managers.player.creation.PlayerCreationManager",
		"server.zone.ZonePacketHandler",
		"server.zone.managers.name.NameManager",
		"server.zone.managers.holocron.HolocronManager",
		"server.zone.managers.sui.SuiManager",
		"server.zone.managers.skill.SkillManager",
		"server.zone.managers.vendor.VendorManager",
		// GroundZone: QuadTree is forward-decl'd with no POD line.
		"server.zone.QuadTree",
		// SpaceZone: Octree (forward-decl no POD) and the
		// ShipObjectTimerTask helper class (an `include`-directive type
		// that the JAR still classifies as a Reference-wrapped IDL
		// class).
		"server.zone.Octree",
		"server.zone.managers.ship.tasks.ShipObjectTimerTask",
	} {
		reg.AddNoPOD(qname)
	}

	// Non-managed parent classes — IDL classes whose own ancestry
	// doesn't reach `ManagedObject`. Subclasses of these (TreeEntry,
	// LambdaObserver) skip the forward-decl layout entirely; every
	// IDL import becomes a regular `#include`.
	for _, name := range []string{
		"engine.util.Observable",
		"engine.util.Observer",
	} {
		reg.AddNonManagedParent(name)
	}

	if m.Class.IsMock {
		// `@mock` mock-method bodies require whole-corpus knowledge to
		// derive (the JAR walks every parent IDL); inject the expected
		// body verbatim from a fixture instead.
		mockPath := filepath.Join(root, "testdata", "mock", className+".mock")
		body, err := os.ReadFile(mockPath)
		if err != nil {
			t.Fatalf("missing mock fixture %s: %v", mockPath, err)
		}
		m.MockBody = string(body)
	}

	gotH, gotC, err := cpp.Generate(m, reg)

	if err != nil {
		t.Fatal(err)
	}

	checkOrUpdate(t, wantHeader, gotH)
	checkOrUpdate(t, wantSource, gotC)
}

func checkOrUpdate(t *testing.T, path string, got []byte) {
	t.Helper()

	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)

	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(want, got) {
		return
	}

	// Persist actual output for offline diffing.
	dump := path + ".got"
	_ = os.WriteFile(dump, got, 0o644)

	if d := unifiedDiff(want, got); d != "" {
		t.Errorf("%s mismatch (got dumped to %s):\n%s", filepath.Base(path), dump, d)
		return
	}

	t.Errorf("%s mismatch (got dumped to %s, %d bytes want vs %d bytes got)",
		filepath.Base(path), dump, len(want), len(got))
}

func unifiedDiff(want, got []byte) string {
	if _, err := exec.LookPath("diff"); err != nil {
		return ""
	}

	wantTmp, _ := os.CreateTemp("", "want-*")
	gotTmp, _ := os.CreateTemp("", "got-*")

	defer os.Remove(wantTmp.Name())
	defer os.Remove(gotTmp.Name())

	wantTmp.Write(want)
	gotTmp.Write(got)
	wantTmp.Close()
	gotTmp.Close()
	cmd := exec.Command("diff", "-u", wantTmp.Name(), gotTmp.Name())
	out, _ := cmd.Output()

	return string(out)
}
