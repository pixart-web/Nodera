package projects

import "testing"

func TestProjectSlugCannotEnterTheControlPlaneNamespace(t *testing.T) {
	for _, slug := range []string{"cp-api", "cp-postgres", "cp-x"} {
		in := ProjectInput{Name: "x", Slug: slug, Kind: "wordpress"}
		if err := in.validate(); err == nil {
			t.Errorf("slug %q must be rejected: managed container nodera-%s would sit in the protected namespace", slug, slug)
		}
	}
	// A name that merely derives a reserved slug is rejected too, not silently renamed.
	in := ProjectInput{Name: "CP API", Kind: "wordpress"}
	if err := in.validate(); err == nil {
		t.Errorf("derived slug %q must be rejected", in.Slug)
	}
	for _, slug := range []string{"cpanel", "my-cp-site", "api", "blog"} {
		in := ProjectInput{Name: "x", Slug: slug, Kind: "wordpress"}
		if err := in.validate(); err != nil {
			t.Errorf("slug %q should be allowed: %v", slug, err)
		}
	}
}
