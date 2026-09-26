package action

import (
	"errors"
	"reflect"
	"testing"
)

type fakeDefSource struct {
	files map[string]string
	pkgs  map[string]PackageContext
}

func (s *fakeDefSource) Load(a, t string) ([]byte, error) {
	if f, ok := s.files[a+"/"+t]; ok {
		return []byte(f), nil
	}
	return nil, errors.New("not installed")
}
func (s *fakeDefSource) List() ([]string, error) { return []string{"acme/greet"}, nil }
func (s *fakeDefSource) Package(a string) PackageContext {
	if p, ok := s.pkgs[a]; ok {
		return p
	}
	return nil
}

func TestLoaderUsesDefSource(t *testing.T) {
	pkg := &fakePkg{id: "acme", startURL: "https://acme.com/"}
	SetDefSource(&fakeDefSource{
		files: map[string]string{"acme/greet": `{"actionType":"greet","automation":"acme","sideEffects":"none",
			"steps":[{"id":"hi","type":"set_variable","variable":"where","value":"{{site.startUrl}}"}]}`},
		pkgs: map[string]PackageContext{"acme": pkg},
	})
	t.Cleanup(func() { SetDefSource(nil) })

	def, err := GetLoader().Load("acme", "greet")
	if err != nil {
		t.Fatal(err)
	}
	if def.Platform != "acme" {
		t.Errorf("automation alias: Platform = %q", def.Platform)
	}
	if _, err := GetLoader().Load("instagram", "send_dms"); err == nil {
		t.Error("with a DefSource the embedded seed must not be read")
	}
	list, _ := GetLoader().ListAvailable()
	if !reflect.DeepEqual(list, []string{"acme/greet"}) {
		t.Errorf("ListAvailable = %v", list)
	}

	ae := newPkgExecutor(nil, nil)
	if _, err := ae.Execute(&StorageAction{ID: "a", TargetPlatform: "acme", Type: "greet"}); err != nil {
		t.Fatal(err)
	}
	if ae.Package() != pkg {
		t.Error("Execute did not attach the package from the DefSource")
	}
	if v, _ := ae.execCtx.GetVariable("where"); v != "https://acme.com/" {
		t.Errorf("site.startUrl = %v", v)
	}
}

// genSource counts Loads and reports a settable generation.
type genSource struct {
	fakeDefSource
	gen   string
	loads int
}

func (s *genSource) Generation() string { return s.gen }
func (s *genSource) Load(a, t string) ([]byte, error) {
	s.loads++
	return s.fakeDefSource.Load(a, t)
}

func TestLoaderCacheFollowsSourceGeneration(t *testing.T) {
	src := &genSource{gen: "1", fakeDefSource: fakeDefSource{files: map[string]string{
		"acme/greet": `{"actionType":"greet","automation":"acme","version":"1","steps":[]}`}}}
	SetDefSource(src)
	t.Cleanup(func() { SetDefSource(nil) })
	l := GetLoader()

	d1, _ := l.Load("acme", "greet")
	d2, _ := l.Load("acme", "greet")
	if src.loads != 1 || d1 != d2 {
		t.Fatalf("same generation should hit the cache (loads=%d)", src.loads)
	}
	// An update bumps the generation: the new definition is served.
	src.files["acme/greet"] = `{"actionType":"greet","automation":"acme","version":"2","steps":[]}`
	src.gen = "2"
	if d, _ := l.Load("acme", "greet"); d.Version != "2" {
		t.Fatalf("stale definition after update: %q", d.Version)
	}
	// Uninstall: fail rather than serve the cache.
	delete(src.files, "acme/greet")
	src.gen = "3"
	if _, err := l.Load("acme", "greet"); err == nil {
		t.Fatal("removed package still loads")
	}
}

func TestLoaderWithoutGenerationAlwaysAsksSource(t *testing.T) {
	src := &fakeDefSource{files: map[string]string{"acme/greet": `{"actionType":"greet","steps":[]}`}}
	SetDefSource(src)
	t.Cleanup(func() { SetDefSource(nil) })
	if _, err := GetLoader().Load("acme", "greet"); err != nil {
		t.Fatal(err)
	}
	delete(src.files, "acme/greet")
	if _, err := GetLoader().Load("acme", "greet"); err == nil {
		t.Fatal("a source without Generation must be consulted on every Load")
	}
}
