package sitemig

import (
 "bytes"
 "context"
 "errors"
 "fmt"
 "io"
 "net"
 "path"
 "sort"
 "strconv"
 "strings"
 "time"
 "unicode"

 "github.com/pkg/sftp"
 "golang.org/x/crypto/ssh"
 "github.com/nodera/nodera/internal/providers"
)

type remoteConfig struct { Host string; Port int; User string; Path string; DBName string; DBUser string; DBPassword string; Key string; Password string; HostKey string }

func remoteCfg(cfg map[string]any, creds map[string]string) (remoteConfig,error) {
 r:=remoteConfig{Port:22,Path:"/"}; get:=func(k string)string{if v,ok:=cfg[k].(string);ok&&v!=""{return v};return creds[k]}
 r.Host=get("host");r.User=get("user");r.Path=get("path");r.DBName=get("db_name");r.DBUser=get("db_user");r.DBPassword=get("db_password");r.Key=get("private_key");r.Password=get("password");r.HostKey=get("host_key_sha256")
 if v:=get("port");v!=""{n,e:=strconv.Atoi(v);if e!=nil||n<1||n>65535{return r,fmt.Errorf("invalid port")};r.Port=n}
 if r.Host==""||r.User==""||r.Path==""{return r,fmt.Errorf("host, user and path are required")}
 if r.Key==""&&r.Password==""{return r,fmt.Errorf("private_key or password is required")}
 if r.HostKey==""{return r,fmt.Errorf("host_key_sha256 is required")}
 return r,nil
}
func openSSH(ctx context.Context,c remoteConfig)(*ssh.Client,error){
 var auth ssh.AuthMethod
 if c.Key!=""{signer,e:=ssh.ParsePrivateKey([]byte(c.Key));if e!=nil{return nil,fmt.Errorf("invalid private key")};auth=ssh.PublicKeys(signer)}else{auth=ssh.Password(c.Password)}
 f:=strings.Fields(c.HostKey);fp:=f[len(f)-1];if !strings.HasPrefix(fp,"SHA256:"){return nil,fmt.Errorf("invalid host key fingerprint")}
 cc:=&ssh.ClientConfig{User:c.User,Auth:[]ssh.AuthMethod{auth},Timeout:20*time.Second,HostKeyCallback:func(_ string,_ net.Addr,k ssh.PublicKey)error{if ssh.FingerprintSHA256(k)!=fp{return errors.New("remote host key mismatch")};return nil}}
 d:=net.Dialer{Timeout:20*time.Second};conn,e:=d.DialContext(ctx,"tcp",net.JoinHostPort(c.Host,strconv.Itoa(c.Port)));if e!=nil{return nil,e}
 sc,ch,req,e:=ssh.NewClientConn(conn,net.JoinHostPort(c.Host,strconv.Itoa(c.Port)),cc);if e!=nil{conn.Close();return nil,e};return ssh.NewClient(sc,ch,req),nil
}
func remoteSafe(p string)bool{if p==""||strings.ContainsAny(p,"\x00\\"){return false};for _,s:=range strings.Split(path.Clean(p),"/"){if s==".." {return false}};return true}
func validDBIdent(s string)bool{if s==""||len(s)>64{return false};for _,r:=range s{if !(unicode.IsLetter(r)||unicode.IsDigit(r)||r=='_'||r=='-'){return false}};return true}

func pullRemote(ctx context.Context,c remoteConfig,fs providers.FilesystemProvider,dest string)(SourceInfo,error){
 client,e:=openSSH(ctx,c);if e!=nil{return SourceInfo{},e};defer client.Close();sc,e:=sftp.NewClient(client);if e!=nil{return SourceInfo{},e};defer sc.Close()
 root:=path.Clean(c.Path);var info SourceInfo;count:=0
 var walk func(string,string)error
 walk=func(rp,rel string)error{ents,e:=sc.ReadDir(rp);if e!=nil{return e};sort.Slice(ents,func(i,j int)bool{return ents[i].Name()<ents[j].Name()});for _,ent:=range ents{name:=ent.Name();if name=="."||name==".."{continue};rr:=path.Join(rel,name);if !remoteSafe(rr){return fmt.Errorf("unsafe remote path")};full:=path.Join(rp,name);if ent.IsDir(){if e:=walk(full,rr);e!=nil{return e};continue};if count>=20000{return fmt.Errorf("source exceeds 20000 files")};f,e:=sc.Open(full);if e!=nil{return e};b,e:=io.ReadAll(io.LimitReader(f,20<<20+1));f.Close();if e!=nil{return e};if len(b)>20<<20{return fmt.Errorf("source file exceeds 20 MiB: %s",rr)};if e:=fs.WriteFile(ctx,path.Join(dest,"files",rr),b);e!=nil{return e};info.Files++;info.SizeBytes+=int64(len(b));count++;if rr=="wp-config.php"{info.IsWordPress=true};if rr=="wp-includes/version.php"{info.IsWordPress=true}};return nil}
 if e:=walk(root,"");e!=nil{return info,e}
 if c.DBName!=""{if e:=dumpRemoteDB(ctx,client,c,fs,dest);e!=nil{return info,e};info.HasDatabase=true}
 return info,nil
}
func shellQuote(s string)string{return "'"+strings.ReplaceAll(s,"'","'\\''")+"'"}
func dumpRemoteDB(ctx context.Context,client *ssh.Client,c remoteConfig,fs providers.FilesystemProvider,dest string)error{
 if !validDBIdent(c.DBName)||!validDBIdent(c.DBUser){return fmt.Errorf("invalid database identifier")}
 sess,e:=client.NewSession();if e!=nil{return e};defer sess.Close();var out bytes.Buffer;sess.Stdout=&out
 cmd:="mysqldump --single-transaction --quick --routines --triggers --no-tablespaces -u "+shellQuote(c.DBUser)+" -p"+shellQuote(c.DBPassword)+" "+shellQuote(c.DBName)
 if e:=sess.Run(cmd);e!=nil{return fmt.Errorf("database dump failed: %w",e)};if len(out.Bytes())>512<<20{return fmt.Errorf("database dump exceeds 512 MiB")};return fs.WriteFile(ctx,path.Join(dest,"database.sql"),out.Bytes())
}

type SFTPConnector struct{}
func (SFTPConnector) Discover(ctx context.Context,cfg map[string]any,creds map[string]string)(SourceInfo,error){
 c,e:=remoteCfg(cfg,creds);if e!=nil{return SourceInfo{},e};cl,e:=openSSH(ctx,c);if e!=nil{return SourceInfo{},e};defer cl.Close();sc,e:=sftp.NewClient(cl);if e!=nil{return SourceInfo{},e};defer sc.Close();es,e:=sc.ReadDir(c.Path);if e!=nil{return SourceInfo{},e};info:=SourceInfo{Files:len(es),HasDatabase:c.DBName!=""};for _,x:=range es{if x.Name()=="wp-config.php"||x.Name()=="wp-content"{info.IsWordPress=true}};return info,nil
}
func (SFTPConnector) Pull(ctx context.Context,cfg map[string]any,creds map[string]string,fs providers.FilesystemProvider,dest string)error{c,e:=remoteCfg(cfg,creds);if e!=nil{return e};_,e=pullRemote(ctx,c,fs,dest);return e}
type SSHConnector struct{}
func (SSHConnector) Discover(ctx context.Context,cfg map[string]any,creds map[string]string)(SourceInfo,error){return SFTPConnector{}.Discover(ctx,cfg,creds)}
func (SSHConnector) Pull(ctx context.Context,cfg map[string]any,creds map[string]string,fs providers.FilesystemProvider,dest string)error{c,e:=remoteCfg(cfg,creds);if e!=nil{return e};_,e=pullRemote(ctx,c,fs,dest);return e}
type CPanelConnector struct{}
func (CPanelConnector) Discover(ctx context.Context,cfg map[string]any,creds map[string]string)(SourceInfo,error){if _,ok:=cfg["path"];!ok{cfg["path"]="/home/"+creds["user"]+"/public_html"};return SSHConnector{}.Discover(ctx,cfg,creds)}
func (CPanelConnector) Pull(ctx context.Context,cfg map[string]any,creds map[string]string,fs providers.FilesystemProvider,dest string)error{if _,ok:=cfg["path"];!ok{cfg["path"]="/home/"+creds["user"]+"/public_html"};return SSHConnector{}.Pull(ctx,cfg,creds,fs,dest)}
var _ Connector=SFTPConnector{}
var _ Connector=SSHConnector{}
var _ Connector=CPanelConnector{}
