package github

import (
 "archive/tar"
 "compress/gzip"
 "context"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "net/url"
 "path"
 "strings"
 "time"

 "github.com/nodera/nodera/internal/providers"
)

type Provider struct { token, base string; client *http.Client }

func New(token string) *Provider {
 return &Provider{token:strings.TrimSpace(token), base:"https://api.github.com", client:&http.Client{Timeout:90*time.Second}}
}
func (p *Provider) request(ctx context.Context, method, endpoint string, body io.Reader) (*http.Response,error) {
 req,err:=http.NewRequestWithContext(ctx,method,p.base+endpoint,body); if err!=nil{return nil,err}
 req.Header.Set("Accept","application/vnd.github+json"); req.Header.Set("X-GitHub-Api-Version","2022-11-28")
 if p.token!=""{req.Header.Set("Authorization","Bearer "+p.token)}
 resp,err:=p.client.Do(req);if err!=nil{return nil,fmt.Errorf("github request: %w",err)}
 if resp.StatusCode<200||resp.StatusCode>=300{b,_:=io.ReadAll(io.LimitReader(resp.Body,4096));resp.Body.Close();return nil,fmt.Errorf("github API %s: %s",resp.Status,strings.TrimSpace(string(b)))}
 return resp,nil
}
func validRepo(s string)error{p:=strings.Split(s,"/");if len(p)!=2||p[0]==""||p[1]==""||strings.ContainsAny(s,"\\\n\r\x00 "){return fmt.Errorf("repository must be owner/name")};return nil}
func validRef(s string)bool{return s!=""&&!strings.Contains(s,"..")&&!strings.ContainsAny(s,"\\\n\r\x00")&&!strings.HasPrefix(s,"/")&&!strings.HasSuffix(s,"/")}
func safePath(s string)bool{if s==""||strings.HasPrefix(s,"/")||strings.ContainsAny(s,"\\\x00"){return false};for _,x:=range strings.Split(s,"/"){if x==""||x==".."{return false}};return true}
func str(m map[string]any,k string)string{v,_:=m[k].(string);return v}
func (p *Provider) ListRepositories(ctx context.Context)([]providers.Repository,error){
 out:=[]providers.Repository{}
 for page:=1;page<=100;page++{resp,err:=p.request(ctx,"GET",fmt.Sprintf("/user/repos?per_page=100&page=%d&sort=updated",page),nil);if err!=nil{return nil,err};var rows []map[string]any;err=json.NewDecoder(resp.Body).Decode(&rows);resp.Body.Close();if err!=nil{return nil,err};for _,r:=range rows{priv,_:=r["private"].(bool);out=append(out,providers.Repository{FullName:str(r,"full_name"),DefaultBranch:str(r,"default_branch"),Private:priv})};if len(rows)<100{break}}
 return out,nil
}
func (p *Provider) ListBranches(ctx context.Context,repo string)([]string,error){
 if err:=validRepo(repo);err!=nil{return nil,err};resp,err:=p.request(ctx,"GET","/repos/"+repo+"/branches?per_page=100",nil);if err!=nil{return nil,err};defer resp.Body.Close();var rows []map[string]any;if err:=json.NewDecoder(resp.Body).Decode(&rows);err!=nil{return nil,err};out:=make([]string,0,len(rows));for _,r:=range rows{out=append(out,str(r,"name"))};return out,nil
}
func (p *Provider) ListCommits(ctx context.Context,repo,branch string,limit int)([]providers.Commit,error){
 if err:=validRepo(repo);err!=nil{return nil,err};if !validRef(branch){return nil,fmt.Errorf("invalid branch")};if limit<1||limit>100{limit=20};resp,err:=p.request(ctx,"GET","/repos/"+repo+"/commits?sha="+url.QueryEscape(branch)+fmt.Sprintf("&per_page=%d",limit),nil);if err!=nil{return nil,err};defer resp.Body.Close()
 var rows []map[string]any;if err:=json.NewDecoder(resp.Body).Decode(&rows);err!=nil{return nil,err};out:=make([]providers.Commit,0,len(rows))
 for _,r:=range rows{cm,_:=r["commit"].(map[string]any);au,_:=cm["author"].(map[string]any);tm,_:=time.Parse(time.RFC3339,str(au,"date"));out=append(out,providers.Commit{SHA:str(r,"sha"),Message:str(cm,"message"),Author:str(au,"name"),At:tm})};return out,nil
}
func (p *Provider) Fetch(ctx context.Context,repo,ref string,dest providers.FilesystemProvider,destPath string)(string,error){
 if err:=validRepo(repo);err!=nil{return "",err};if !validRef(ref){return "",fmt.Errorf("invalid git ref")};resp,err:=p.request(ctx,"GET","/repos/"+repo+"/tarball/"+url.PathEscape(ref),nil);if err!=nil{return "",err};defer resp.Body.Close();gz,err:=gzip.NewReader(resp.Body);if err!=nil{return "",fmt.Errorf("github tarball: %w",err)};defer gz.Close();tr:=tar.NewReader(gz);count:=0;var total int64
 for{h,err:=tr.Next();if err==io.EOF{break};if err!=nil{return "",fmt.Errorf("github tarball: %w",err)};parts:=strings.SplitN(h.Name,"/",2);if len(parts)!=2||h.Typeflag==tar.TypeDir{continue};name:=parts[1];if !safePath(name){continue};if count>=20000{return "",fmt.Errorf("repository exceeds 20000 files")};b,err:=io.ReadAll(io.LimitReader(tr,20<<20+1));if err!=nil{return "",err};if len(b)>20<<20{return "",fmt.Errorf("repository file exceeds 20 MiB: %s",name)};total+=int64(len(b));if total>512<<20{return "",fmt.Errorf("repository exceeds 512 MiB")};if err:=dest.WriteFile(ctx,path.Join(destPath,name),b);err!=nil{return "",err};count++}
 resp2,err:=p.request(ctx,"GET","/repos/"+repo+"/commits/"+url.PathEscape(ref),nil);if err==nil{var c map[string]any;if json.NewDecoder(resp2.Body).Decode(&c)==nil{resp2.Body.Close();if s:=str(c,"sha");s!=""{return s,nil}}else{resp2.Body.Close()}}
 return ref,nil
}
func (p *Provider) CreateWebhook(ctx context.Context,repo,callback string)(string,error){
 if err:=validRepo(repo);err!=nil{return "",err};u,err:=url.Parse(callback);if err!=nil||u.Scheme!="https"||u.Host==""{return "",fmt.Errorf("callback URL must be HTTPS")};body:=strings.NewReader(fmt.Sprintf("{\"name\":\"web\",\"active\":true,\"events\":[\"push\"],\"config\":{\"url\":%q,\"content_type\":\"json\",\"insecure_ssl\":\"0\"}}",callback));resp,err:=p.request(ctx,"POST","/repos/"+repo+"/hooks",body);if err!=nil{return "",err};defer resp.Body.Close();var v map[string]any;if err:=json.NewDecoder(resp.Body).Decode(&v);err!=nil{return "",err};id,_:=v["id"].(float64);return fmt.Sprintf("%.0f",id),nil
}
var _ providers.GitProvider=(*Provider)(nil)
