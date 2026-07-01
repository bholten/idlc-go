package golden_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestCorpusLockDispatchParity guards the single most dangerous class of
// silent regression in this compiler: emitting the wrong lock/dispatch shape
// for a generated method. That is exactly the bug that shipped once — a
// @preLocked assert emitted on a method that must not have it — and the
// golden/probe suites missed it because their oracles had been regenerated
// from idlc-go itself.
//
// This test uses the JAR as the oracle over the WHOLE Core3 corpus. It does
// NOT byte-compare (idlc-go is only functionally equivalent to the JAR — it
// diverges cosmetically in ~200 files on comments, inline-body formatting and
// field ordering). Instead it extracts a per-method *semantic fingerprint* —
// the read-vs-write impl fetch, the `this`-lock assert, and the arg-preLocked
// asserts — and requires it to match the JAR for every method present in both.
//
// Inputs are the two baseline trees produced by `make baseline` (JAR +
// idlc-go, generated from the same Core3 checkout). Both are gitignored: the
// JAR is an unlicensed proprietary blob we keep out of the tree, so this test
// SKIPS when the baselines are absent (local runs without a Core3 checkout)
// and only enforces in environments that ran `make baseline` first.
func TestCorpusLockDispatchParity(t *testing.T) {
	root := repoRoot(t)
	jarDir := filepath.Join(root, "_baseline", "jar")
	goDir := filepath.Join(root, "_baseline", "idlc-go")

	if !isDir(jarDir) || !isDir(goDir) {
		t.Skipf("corpus baselines missing (%s / %s); run `make baseline` "+
			"(needs a Core3 checkout with idlc.jar) to enable this test", jarDir, goDir)
	}

	cpps := collectRel(t, jarDir, ".cpp")
	if len(cpps) == 0 {
		t.Fatalf("no .cpp files under %s — is the JAR baseline populated?", jarDir)
	}

	var (
		fpMismatches int
		droppedTotal int
		filesChecked int
	)

	for _, rel := range cpps {
		goPath := filepath.Join(goDir, rel)
		if !fileExists(goPath) {
			t.Errorf("%s: present in JAR baseline but idlc-go produced no output", rel)
			continue
		}
		filesChecked++

		jarFP := lockFingerprints(t, filepath.Join(jarDir, rel))
		goFP := lockFingerprints(t, goPath)

		for key, jf := range jarFP {
			gf, ok := goFP[key]
			if !ok {
				// A dispatch method the JAR emits but idlc-go dropped — a real
				// regression (e.g. a whole @preLocked method vanishing).
				droppedTotal++
				t.Errorf("%s: method %q present in JAR but missing from idlc-go", rel, key)
				continue
			}
			if jf != gf {
				fpMismatches++
				t.Errorf("%s: method %q lock/dispatch fingerprint diverges from JAR:\n"+
					"    JAR:     fetch=%s thisLock=%v argLocks=%v\n"+
					"    idlc-go: fetch=%s thisLock=%v argLocks=%v",
					rel, key, jf.fetch, jf.thisLock, jf.argLocks,
					gf.fetch, gf.thisLock, gf.argLocks)
			}
		}
		// Methods only in idlc-go (extra helpers the JAR emits elsewhere) are a
		// known, benign divergence and are intentionally not failed here.
	}

	t.Logf("corpus lock/dispatch parity: %d .cpp files, %d fingerprint mismatches, %d dropped methods",
		filesChecked, fpMismatches, droppedTotal)
}

// lockFingerprint is the semantic shape of a single generated dispatch stub.
type lockFingerprint struct {
	fetch    string // "read" (_getImplementationForRead) or "write" (_getImplementation)
	thisLock bool   // emits assert(this->isLockedByCurrentThread())
	argLocks string // sorted, comma-joined arg names with a preLocked assert
}

// A generated stub definition opener, e.g.
//
//	Reference<WeaponObject* > CreatureObject::getWeapon() {
//	void TangibleObject::addTemplateSkillMods(TangibleObject* targetObject) const {
//
// Group 1 = Class::method, group 2 = raw params, group 3 = optional const.
var stubSigRe = regexp.MustCompile(`^[A-Za-z][\w:<>,\*\s&]*?\b(\w+::\w+)\s*\(([^;{]*)\)\s*(const)?\s*\{`)

// arg-preLocked assert: `assert((foo == NULL) || bar->isLockedByCurrentThread());`
// RE2 has no backreferences, so we capture both names and compare in code.
var argLockRe = regexp.MustCompile(`assert\(\((\w+) == NULL\) \|\| (\w+)->isLockedByCurrentThread`)

var thisLockRe = regexp.MustCompile(`assert\(this->isLockedByCurrentThread`)

// idlc-go and the JAR legitimately differ on `const` — both its ordering
// (`unsigned const int` vs `const unsigned int`) and, on some params, its
// presence at all (the JAR emits `const String&`, idlc-go `String&`). Cancel
// `const` entirely from the signature key so the same method matches up; keep
// `unsigned`/`signed`, which are part of the parameter's type identity.
var constRe = regexp.MustCompile(`\bconst\b`)
var wsRe = regexp.MustCompile(`\s+`)

// lockFingerprints parses a generated .cpp and returns the lock/dispatch
// fingerprint of every dispatch stub it contains, keyed by a normalized
// signature (method name + params with qualifier order/whitespace canceled,
// plus the const-ness) so cosmetic-only differences don't create false keys.
func lockFingerprints(t *testing.T, path string) map[string]lockFingerprint {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(data), "\n")
	out := make(map[string]lockFingerprint)

	for i := 0; i < len(lines); i++ {
		m := stubSigRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		// Collect the body up to the next line that starts a top-level `}`.
		var body strings.Builder
		j := i + 1
		for j < len(lines) && !strings.HasPrefix(lines[j], "}") {
			body.WriteString(lines[j])
			body.WriteByte('\n')
			j++
		}
		i = j
		btext := body.String()

		// Only dispatch stubs carry a lock/dispatch shape.
		if !strings.Contains(btext, "_getImplementation") {
			continue
		}

		fetch := "write"
		if strings.Contains(btext, "_getImplementationForRead()") {
			fetch = "read"
		}

		var args []string
		for _, a := range argLockRe.FindAllStringSubmatch(btext, -1) {
			if a[1] == a[2] { // same var on both sides of the assert
				args = append(args, a[1])
			}
		}
		sort.Strings(args)
		args = dedup(args)

		key := normalizeSig(m[1], m[2])
		out[key] = lockFingerprint{
			fetch:    fetch,
			thisLock: thisLockRe.MatchString(btext),
			argLocks: strings.Join(args, ","),
		}
	}
	return out
}

// normalizeSig cancels the two known cosmetic axes on which idlc-go and the
// JAR legitimately differ — qualifier ordering (`unsigned const int` vs
// `const unsigned int`) and whitespace — so the key identifies the method,
// not its rendering.
func normalizeSig(method, params string) string {
	p := constRe.ReplaceAllString(params, "")
	p = wsRe.ReplaceAllString(p, "")
	// Trailing const on the member function itself is intentionally NOT part
	// of the key: it never affects lock/dispatch shape and is another axis on
	// which idlc-go can render differently.
	return method + "(" + p + ")"
}

func dedup(s []string) []string {
	if len(s) < 2 {
		return s
	}
	out := s[:1]
	for _, v := range s[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// collectRel returns every path under root with the given extension, relative
// to root, sorted for deterministic iteration.
func collectRel(t *testing.T, root, ext string) []string {
	t.Helper()
	var rels []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, ext) {
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return rerr
			}
			rels = append(rels, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(rels)
	return rels
}
