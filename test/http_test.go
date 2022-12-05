//基本的GET请求
package test;
 
import (
 "fmt"
 "errors"
 "io"
 "io/ioutil"
 "strings"
 "bytes"
 "net/http"
 "net/url"
 "time"
 "runtime"
 "syscall"
 "testing"
)

type DavError struct {
	Code		int
	Message		string
	Location	string
	Errnum		syscall.Errno
}

var ReadBuff = 1024
var Url = "http://192.168.3.1:8080"
var Username = "admin"
var Password = "admin"
var Cookie = ""
var IsSabre bool
var IsApache bool
var cc *http.Client
var Methods map[string]bool
var DavSupport map[string]bool
var davTimeFormat = "2006-01-02T15:04:05Z"

var davToErrnoMap = map[int]syscall.Errno{
	403:	syscall.EACCES,
	404:	syscall.ENOENT,
	405:	syscall.EACCES,
	408:	syscall.ETIMEDOUT,
	409:	syscall.ENOENT,
	416:	syscall.ERANGE,
	504:	syscall.ETIMEDOUT,
}

//func (d *DavError) Errno() fuse.Errno {
//	return fuse.Errno(d.Errnum)
//}

func (d *DavError) Error() string {
	return d.Message
}

func davToErrno(err *DavError) (*DavError) {
	if fe, ok := davToErrnoMap[err.Code]; ok {
		err.Errnum = fe
		return err
	}
	err.Errnum = syscall.EIO
	return err
}

func statusIsValid(resp *http.Response) bool {
	return resp.StatusCode / 100 == 2
}

func statusIsRedirect(resp *http.Response) bool {
	return resp.StatusCode / 100 == 3
}

func stripQuotes(s string) string {
	l := len(s)
	if l > 1 && s[0] == '"' && s [l-1] == '"' {
		return s[1:l-1]
	}
	return s
}

func stripLastSlash(s string) string {
	l := len(s)
	for l > 0 {
		if s[l-1] != '/' {
			return s[:l]
		}
		l--
	}
	return s
}

func addSlash(s string) string {
	if len(s) > 0 && s[len(s)-1] != '/' {
		s += "/"
	}
	return s
}

func dirName(s string) string {
	s = stripLastSlash(s)
	i := strings.LastIndex(s, "/")
	if i > 0 {
		return s[:i]
	}
	return "/"
}

func parseTime (s string) (t time.Time) {
	if len(s) > 0 && s[0] >= '0' && s[0] <= '9' {
		t, _ = time.Parse(davTimeFormat, s)
	} else {
		t, _ = http.ParseTime(s)
	}
	return
}

func joinPath(s1, s2 string) string {
	if (len(s1) > 0 && s1[len(s1)-1] == '/') ||
	   (len(s2) > 0 && s2[0] == '/') {
		return s1 + s2
	}
	return s1 + "/" + s2
}

func stripHrefPrefix(href string, prefix string) (string, bool) {
	u, _ := url.ParseRequestURI(href)
	if u == nil {
		return "", false
	}
	name := u.Path
	if strings.HasPrefix(name, prefix) {
		name = name[len(prefix):]
	}
	i := strings.Index(name, "/")
	if i >= 0 && i < len(name) - 1 {
		return "", false
	}
	return name, true
}

func mapLine(s string) (m map[string]bool) {
	m = make(map[string]bool)
	elems := strings.Split(s, ",")
	for _, e := range elems {
		e = strings.TrimSpace(e)
		if e != "" {
			m[e] = true
		}
	}
	return
}

func getHeader(h http.Header, key string) string {
	key = http.CanonicalHeaderKey(key)
	return strings.Join(h[key], ",")
}

func Init() (err error) {
	if cc == nil {
		Url = stripLastSlash(Url)
		var u *url.URL
		u, err = url.ParseRequestURI(Url)
		if err != nil {
			fmt.Println("u=(s)", u)
			return
		}
		//d.base = u.Path

		// Override some values from DefaultTransport.
		tr := *(http.DefaultTransport.(*http.Transport))
		tr.MaxIdleConns = 100
		tr.MaxConnsPerHost = 100
		tr.MaxIdleConnsPerHost = 100
		tr.DisableCompression = true

		cc = &http.Client{
			Timeout: 60 * time.Second,
			Transport: &tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return errors.New("400 Will not follow redirect")
			},
		}
	}
	req, err := buildRequest("OPTIONS", "/")
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "*/*")
	resp, err := do(req)
	defer drainBody(resp)
	if err != nil {
		return
	}
	if !statusIsValid(resp) {
		err = errors.New(resp.Status)
		return
	}

	// Parse headers.
	Methods = mapLine(getHeader(resp.Header, "Allow"))
	DavSupport = mapLine(getHeader(resp.Header, "Dav"))

	// Is this apache with mod_dav?
	isApache := strings.Index(resp.Header.Get("Server"), "Apache") >= 0
	if isApache && DavSupport["<http://apache.org/dav/propset/fs/1>"] {
		IsApache = true
	}

	// Does this server supoort sabredav-partialupdate ?
	if DavSupport["sabredav-partialupdate"] {
		IsSabre = true
	}

	if !DavSupport["1"] {
		err = errors.New("not a webdav server")
	}

	// check if it exists and is a directory.
	/*
	if err == nil {
		var dnode Dnode
		dnode, err = d.Stat("/")
		if err == nil && !dnode.IsDir {
			err = errors.New(d.Url + " is not a directory")
		}
	}*/

	return
}

var userAgent = fmt.Sprintf("fuse-webdavfs/0.1 (Go) %s (%s)", runtime.GOOS, runtime.GOARCH)
func do(req *http.Request) (resp *http.Response, err error) {
	req.Header.Set("User-Agent", userAgent)

	if trace(T_HTTP_REQUEST) {
		tPrintf("%s %s HTTP/1.1", req.Method, req.URL.String())
		if trace(T_HTTP_HEADERS) {
			tPrintf("%s", tHeaders(req.Header, " "))
		}
		defer func() {
			if err != nil {
				tPrintf("%s request error: %v", req.Method, err)
			} else {
				tPrintf("%s %s", resp.Proto, resp.Status)
				if trace(T_HTTP_HEADERS) {
					tPrintf("%s", tHeaders(resp.Header, " "))
				}
			}
		}()
	}

	resp, err = cc.Do(req)
	if err == nil && !statusIsValid(resp) {
		err = davToErrno(&DavError{
			Message: resp.Status,
			Code: resp.StatusCode,
			Location: resp.Header.Get("Location"),
		})
	}
	return
}

func drainBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	defer resp.Body.Close()
	//reader := bufio.NewReaderSize(resp.Body, int(d.ReadBuff))
	b := make([]byte, ReadBuff)
	var err error
	for err != io.EOF {
		_, err = resp.Body.Read(b)
	}
	//resp.Body.Close()
	resp.Body = nil
}

func buildRequest(method string, path string, b ...interface{}) (req *http.Request, err error) {
	if len(path) == 0 || path[0] != '/' {
		err = errors.New("path does not start with /")
		return
	}
	var body io.Reader
	blen := 0
	if len(b) > 0 && b[0] != nil {
		switch v := b[0].(type) {
		case string:
			body = strings.NewReader(v)
			blen = len(v)
		case []byte:
			body = bytes.NewReader(v)
			blen = len(v)
		default:
			body = v.(io.Reader)
			blen = -1
		}
	}
	u := url.URL{ Path: path }
	req, err = http.NewRequest(method, Url + u.EscapedPath(), body)
	if err != nil {
		return
	}
	if (blen >= 0) {
		if blen == 0 {
			// Need this to FORCE the http client to send a
			// Content-Length header for size 0.
			req.TransferEncoding = []string{"identity"}
		}
		req.ContentLength = int64(blen)
	}
	if Username != "" || Password != "" {
		req.SetBasicAuth(Username, Password)
	}
	if Cookie != "" {
		req.Header.Set("Cookie", Cookie)
	}
	return
}

func GetRange(path string, offset int64, length int) (data []byte, err error) {
	//d.semAcquire()
	//defer d.semRelease()

	if trace(T_WEBDAV) && length >= 0 {
		tPrintf("GetRange(%s, %d, %d)", path, offset, length)
		defer func() {
			if err != nil {
				tPrintf("GetRange: %v", err)
				return
			}
			tPrintf("GetRange: returns %d bytes", len(data))
		}()
	}
	req, err := buildRequest("GET", path)
	if err != nil {
		return
	}
	partial := false
	if (offset >= 0 && length >= 0 ) {
		partial = true
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset + int64(length) - 1))
	}
	resp, err := do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if !statusIsValid(resp) {
		err = errors.New(resp.Status)
		return
	}
	if partial && resp.StatusCode != 206 {
		err = davToErrno(&DavError{
			Message: "416 Range Not Satisfiable",
			Code: 416,
		})
		return
	}
	data, err = ioutil.ReadAll(resp.Body)
	if len(data) > length {
		data = data[:length]
	}
	return
}

func TestHttpGet(t *testing.T) {
	err := Init()
	if err != nil {
		fmt.Println("init failed")
	}
	fmt.Println("init success")
	type ch chan []byte
	var data []byte
	data, err = GetRange("/Games/BROOD109b.zip", 0, 65535000)
	if err == nil {
		fmt.Println("data length = ", len(data)); 
	}else {
		fmt.Println(err)
	}
}

// HTTP get请求
func httpget(ch chan int){
 resp, err := http.Get("http://192.168.3.1:8080/Games/BROOD109b.zip")
 if err != nil {
  fmt.Println(err)
  return
 }
 defer resp.Body.Close()
 body, err := ioutil.ReadAll(resp.Body)
 fmt.Println(string(body))
 fmt.Println(resp.StatusCode)
 if resp.StatusCode == 200 {
  fmt.Println("ok")
 }
 ch <- 1
}
// 主方法
func _TestHttp(t *testing.T) {
 start := time.Now()

 chs := make([]chan int, 20)
 for i := 0; i < 20; i++ {
  chs[i] = make(chan int)
  go httpget(chs[i])
 }
 for _, ch := range chs {
  <- ch
 }
 end := time.Now()
 consume := end.Sub(start).Seconds()
 fmt.Println("consume (s)", consume)
}
