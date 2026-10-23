package projects

import (
	"context"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nodera/nodera/internal/audit"
	"github.com/nodera/nodera/internal/platform/apierr"
	"github.com/nodera/nodera/internal/platform/authctx"
	infraproviders "github.com/nodera/nodera/internal/providers"
	"github.com/nodera/nodera/internal/rbac"
)

type DiscoveredResource struct {
	ContainerName  string     `json:"container_name"`
	Image          string     `json:"image"`
	State          string     `json:"state"`
	Kind           string     `json:"kind"`
	Name           string     `json:"name"`
	Domains        []string   `json:"domains,omitempty"`
	ComposeProject string     `json:"compose_project,omitempty"`
	Adopted        bool       `json:"adopted"`
	ProjectID      *uuid.UUID `json:"project_id,omitempty"`
}

type DiscoveryService struct {
	pool  *pgxpool.Pool
	audit AuditRecorder
}

func NewDiscovery(pool *pgxpool.Pool, a AuditRecorder) *DiscoveryService {
	return &DiscoveryService{pool: pool, audit: a}
}

var hostRuleRe = regexp.MustCompile("(?i)Host\\s*\\(\\s*\\x60([^\\x60]+)\\x60")

func classify(c infraproviders.ContainerInfo) string {
	if strings.Contains(strings.ToLower(c.Image), "wordpress") {
		return "wordpress"
	}
	return "application"
}
func projectName(c infraproviders.ContainerInfo, group string) string {
	if group != "" {
		return strings.Trim(group, "-")
	}
	n := strings.TrimPrefix(c.Name, "/")
	for _, s := range []string{"-wordpress-1", "-web-1", "-api-1", "-worker-1", "-wordpress"} {
		n = strings.TrimSuffix(n, s)
	}
	return n
}
func domains(c infraproviders.ContainerInfo) []string {
	seen := map[string]bool{}
	var out []string
	for k, v := range c.Labels {
		if !strings.Contains(k, "traefik.http.routers.") || !strings.HasSuffix(k, ".rule") {
			continue
		}
		for _, m := range hostRuleRe.FindAllStringSubmatch(v, -1) {
			for _, d := range strings.Split(m[1], ",") {
				d = strings.TrimSpace(strings.ToLower(d))
				if d != "" && !seen[d] {
					seen[d] = true
					out = append(out, d)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func (s *DiscoveryService) Discover(ctx context.Context, ac authctx.AuthContext, cp infraproviders.ContainerProvider) ([]DiscoveredResource, error) {
	if err := rbac.Require(ac, "projects.read"); err != nil {
		return nil, err
	}
	if cp == nil {
		return nil, apierr.Validation("container provider is not configured")
	}
	all, err := cp.List(ctx, nil)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "failed to discover containers", err)
	}
	groups := map[string][]infraproviders.ContainerInfo{}
	for _, c := range all {
		image := strings.ToLower(c.Image)
		if strings.HasPrefix(c.Name, "nodera-cp-") || strings.Contains(c.Name, "_nodera-cp-") || strings.HasPrefix(c.Name, "nodera-staging-") || strings.HasPrefix(c.Name, "nodera-smoke-") || c.Name == "nodera-traefik" || c.Name == "nodera-docker-socket-proxy" || c.Name == "nodera-mariadb" || strings.Contains(image, "nodera-api") || strings.Contains(image, "nodera-agent") {
			continue
		}
		g := c.Labels["com.docker.compose.project"]
		if g == "" {
			g = "standalone:" + c.Name
		}
		groups[g] = append(groups[g], c)
	}
	out := make([]DiscoveredResource, 0, len(groups))
	for group, cs := range groups {
		sort.Slice(cs, func(i, j int) bool {
			a := cs[i].Labels["com.docker.compose.service"]
			b := cs[j].Labels["com.docker.compose.service"]
			rank := func(x string) int {
				if x == "wordpress" || x == "web" {
					return 0
				}
				return 1
			}
			if rank(a) != rank(b) {
				return rank(a) < rank(b)
			}
			return cs[i].Name < cs[j].Name
		})
		rep := cs[0]
		name := projectName(rep, strings.TrimPrefix(group, "standalone:"))
		var pid uuid.UUID
		err := s.pool.QueryRow(ctx, "SELECT id FROM projects WHERE organization_id=$1 AND deleted_at IS NULL AND (config->>'container_name'=$2 OR slug=$3) LIMIT 1", ac.OrganizationID, rep.Name, Slugify(name)).Scan(&pid)
		adopted := err == nil
		if err != nil && err != pgx.ErrNoRows {
			return nil, apierr.Wrap(apierr.CodeInternal, "failed to inspect discovered project", err)
		}
		var pp *uuid.UUID
		if adopted {
			pp = &pid
		}
		out = append(out, DiscoveredResource{ContainerName: rep.Name, Image: rep.Image, State: rep.State, Kind: classify(rep), Name: name, Domains: domains(rep), ComposeProject: strings.TrimPrefix(group, "standalone:"), Adopted: adopted, ProjectID: pp})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

type AdoptInput struct {
	ContainerName string     `json:"container_name"`
	NodeID        *uuid.UUID `json:"node_id"`
}

func (s *DiscoveryService) Adopt(ctx context.Context, ac authctx.AuthContext, in AdoptInput, cp infraproviders.ContainerProvider) (Project, error) {
	if err := rbac.Require(ac, "projects.create"); err != nil {
		return Project{}, err
	}
	if cp == nil {
		return Project{}, apierr.Validation("container provider is not configured")
	}
	c, ok, err := cp.Inspect(ctx, strings.TrimSpace(in.ContainerName))
	if err != nil {
		return Project{}, apierr.Wrap(apierr.CodeInternal, "failed to inspect container", err)
	}
	if !ok {
		return Project{}, apierr.NotFound("container")
	}
	group := c.Labels["com.docker.compose.project"]
	name := projectName(c, group)
	slug := Slugify(name)
	if slug == "" {
		return Project{}, apierr.Validation("could not derive project name")
	}
	ds := domains(c)
	cfg := map[string]any{"container_name": c.Name, "image": c.Image, "domains": ds, "primary_domain": "", "imported": true, "managed_by": "nodera-adoption"}
	if len(ds) > 0 {
		cfg["primary_domain"] = ds[0]
	}
	var id uuid.UUID
	err = s.pool.QueryRow(ctx, "INSERT INTO projects (organization_id,node_id,name,slug,kind,status,description,config) VALUES ($1,$2,$3,$4,$5,'active',$6,$7) ON CONFLICT (organization_id,slug) WHERE deleted_at IS NULL DO UPDATE SET config=projects.config || EXCLUDED.config,status='active',updated_at=now() RETURNING id", ac.OrganizationID, in.NodeID, name, slug, classify(c), "Existing infrastructure adopted by Nodera.", cfg).Scan(&id)
	if err != nil {
		return Project{}, apierr.Wrap(apierr.CodeInternal, "failed to adopt project", err)
	}
	_, _ = s.pool.Exec(ctx, "INSERT INTO applications (organization_id,name,kind,node_id,environment,status,project_id) VALUES ($1,$2,$3,$4,'production',$5,$6) ON CONFLICT (organization_id,name) DO UPDATE SET project_id=EXCLUDED.project_id,node_id=EXCLUDED.node_id,status=EXCLUDED.status,kind=EXCLUDED.kind", ac.OrganizationID, c.Name, classify(c), in.NodeID, c.State, id)
	for _, d := range ds {
		_, _ = s.pool.Exec(ctx, "INSERT INTO domains (organization_id,project_id,name,status,dns_status,ssl_status,dns_provider) VALUES ($1,$2,$3,'active','unknown','unknown','external') ON CONFLICT DO NOTHING", ac.OrganizationID, id, d)
	}
	p, err := scanProject(s.pool.QueryRow(ctx, "SELECT "+projectCols+projectFrom+"WHERE p.id=$1 AND p.organization_id=$2 AND p.deleted_at IS NULL", id, ac.OrganizationID))
	if err != nil {
		return Project{}, apierr.Wrap(apierr.CodeInternal, "failed to load adopted project", err)
	}
	s.audit.Record(ctx, ac, audit.Entry{Action: "projects.project.adopted", ResourceType: "project", ResourceID: id.String(), Success: true, ResultingState: p})
	return p, nil
}
