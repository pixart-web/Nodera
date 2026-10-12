package guard

import (
	"errors"
	"testing"
)

func TestProtectedClassification(t *testing.T) {
	p := Policy{DataRoot: "/srv/nodera/data", ProtectedNames: []string{"traefik", "mariadb"}}
	for _, n := range []string{"nodera-cp-api", "nodera-cp-postgres", "traefik", "mariadb"} {
		if err := p.CheckMutation(n, nil); !errors.Is(err, ErrProtected) {
			t.Errorf("%s must be protected, got %v", n, err)
		}
	}
	if err := p.CheckMutation("nodera-blog", nil); err != nil {
		t.Errorf("an ordinary managed container must be allowed: %v", err)
	}
	// A label marks a container protected even under an innocent name.
	if err := p.CheckMutation("whatever", map[string]string{RoleLabel: RoleControl}); !errors.Is(err, ErrProtected) {
		t.Errorf("labelled container must be protected, got %v", err)
	}
}

func TestCreateRestrictions(t *testing.T) {
	p := Policy{DataRoot: "/srv/nodera/data"}
	ok := [][2]string{{"/srv/nodera/data/projects/blog/data", "/var/www/html"}}
	if err := p.CheckCreate("nodera-blog", nil, []string{"proxy-public"}, ok); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}
	bad := map[string]struct {
		name    string
		labels  map[string]string
		nets    []string
		volumes [][2]string
	}{
		"reserved name":   {"nodera-cp-api", nil, nil, nil},
		"control label":   {"x", map[string]string{RoleLabel: RoleControl}, nil, nil},
		"host network":    {"x", nil, []string{"host"}, nil},
		"docker socket":   {"x", nil, nil, [][2]string{{"/var/run/docker.sock", "/s"}}},
		"root mount":      {"x", nil, nil, [][2]string{{"/", "/host"}}},
		"outside data":    {"x", nil, nil, [][2]string{{"/etc", "/etc"}}},
		"prefix sibling":  {"x", nil, nil, [][2]string{{"/srv/nodera/data-evil", "/x"}}},
		"traversal":       {"x", nil, nil, [][2]string{{"/srv/nodera/data/../../etc", "/x"}}},
		"named volume":    {"x", nil, nil, [][2]string{{"nodera_postgres_data", "/x"}}},
		"relative source": {"x", nil, nil, [][2]string{{"projects/blog/data", "/x"}}},
	}
	for name, c := range bad {
		if err := p.CheckCreate(c.name, c.labels, c.nets, c.volumes); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}
