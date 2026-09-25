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
