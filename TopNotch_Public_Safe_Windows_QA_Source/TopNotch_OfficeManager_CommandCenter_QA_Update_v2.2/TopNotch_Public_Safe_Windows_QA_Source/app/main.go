package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Job struct {
	ID                 string          `json:"id"`
	Revision           int             `json:"revision"`
	Address            string          `json:"address"`
	InspectionDateTime string          `json:"inspectionDateTime"`
	ClientName         string          `json:"clientName"`
	ClientEmail        string          `json:"clientEmail"`
	ClientPhone        string          `json:"clientPhone"`
	AgentName          string          `json:"agentName"`
	Price              float64         `json:"price"`
	Notes              string          `json:"notes"`
	Services           map[string]bool `json:"services"`
	AgreementSigned    bool            `json:"agreementSigned"`
	PaymentComplete    bool            `json:"paymentComplete"`
	WorkspacePrepared  bool            `json:"workspacePrepared"`
	WorkspacePath      string          `json:"workspacePath,omitempty"`
	Inspected          bool            `json:"inspected"`
	Delivered          bool            `json:"delivered"`
	FollowUpComplete   bool            `json:"followUpComplete"`
	Cancelled          bool            `json:"cancelled"`
	Archived           bool            `json:"archived"`
	Updated            string          `json:"updated"`
	Status             string          `json:"status,omitempty"`
	Needs              []string        `json:"needs,omitempty"`
}

type Server struct {
	mu                     sync.Mutex
	base, data, jobs, root string
	html                   []byte
	port                   int
}

var jobIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,180}$`)
var invalidStreet = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
var whitespace = regexp.MustCompile(`\s+`)
var services = []string{"homeInspection", "fourPoint", "windMit", "iaq", "moisture", "mold"}
var forms = map[string][]string{"fourPoint": {"4-Point.pdf", "4-Point Picture Form.docx"}, "windMit": {"Wind Mitigation.pdf", "Wind Mitigation Photo Documentation.docx"}}

func newServer(base string) (*Server, error) {
	s := &Server{base: base, data: filepath.Join(base, "OfficeManager"), jobs: filepath.Join(base, "OfficeManager", "Jobs")}
	if err := os.MkdirAll(s.jobs, 0700); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(base, "App", "office.html"))
	if err != nil {
		return nil, err
	}
	s.html = b
	s.root = filepath.Join(os.Getenv("USERPROFILE"), "Documents", "Inspection Pictures")
	if runtime.GOOS != "windows" {
		if h, e := os.UserHomeDir(); e == nil {
			s.root = filepath.Join(h, "Documents", "Inspection Pictures")
		}
	}
	// The legacy Command Center settings are authoritative when installed.
	// This prevents Office Manager writing to a different Inspection Pictures root.
	settingsFile := filepath.Join(base, "Config", "settings.json")
	if _, statErr := os.Stat(filepath.Join(base, "CommandCenter", "Config", "settings.json")); statErr == nil {
		settingsFile = filepath.Join(base, "CommandCenter", "Config", "settings.json")
	}
	cfg, err := os.ReadFile(settingsFile)
	if err == nil {
		var obj map[string]json.RawMessage
		if json.Unmarshal(cfg, &obj) == nil {
			var r string
			if json.Unmarshal(obj["RootDirectory"], &r) == nil && strings.TrimSpace(r) != "" {
				s.root = r
			}
		}
	}
	return s, nil
}

// Command Center is an independent legacy application. We only verify its
// local installation and launch it; ISN, ASCE, permits and HomeGauge are NOT
// automatically certified by the presence of a launcher.
func (s *Server) commandCenterHome() string {
	candidate := filepath.Join(s.base, "CommandCenter")
	if _, err := os.Stat(filepath.Join(candidate, "RUN_TOP_NOTCH_COMMAND_CENTER.bat")); err == nil {
		return candidate
	}
	return s.base // compatibility with prior releases that bundled it at the root
}
func fileExists(path string) bool {
	x, err := os.Stat(path)
	return err == nil && !x.IsDir()
}
func (s *Server) integrationState() map[string]any {
	cc := s.commandCenterHome()
	launcher := fileExists(filepath.Join(cc, "RUN_TOP_NOTCH_COMMAND_CENTER.bat"))
	script := fileExists(filepath.Join(cc, "App", "TopNotch_CommandCenter.ps1"))
	startup := fileExists(filepath.Join(cc, "Scripts", "Launch.ps1"))
	setup := fileExists(filepath.Join(cc, "Runtime", "setup.ok"))
	available := launcher && script && startup
	templates := map[string]bool{}
	for _, group := range forms {
		for _, fn := range group {
			templates[fn] = fileExists(filepath.Join(s.base, "Templates", fn))
		}
	}
	message := "Command Center installation not found"
	if available {
		message = "Installed; operations and external lookups not yet verified"
		if !setup {
			message = "Installed; first-run setup has not been verified"
		}
	} else if launcher {
		message = "Launcher found, but required scripts are missing"
	}
	return map[string]any{
		"commandCenter": map[string]any{"installed": available, "setupMarkerPresent": setup, "message": message},
		"templates":     templates,
		"isn":           "Not synchronized (read-only integration remains within Command Center)",
		"asce":          "Not verified", "permits": "Not verified", "homeGauge": "Not connected",
	}
}
func street(a string) (string, error) {
	head := strings.TrimSpace(strings.Split(a, ",")[0])
	head = invalidStreet.ReplaceAllString(head, " ")
	head = whitespace.ReplaceAllString(head, " ")
	head = strings.Trim(head, " .")
	if head == "" || head == "." || head == ".." || len(head) > 120 {
		return "", errors.New("Valid street address required (120 characters max)")
	}
	return head, nil
}
func date(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("Valid local inspection date and time required")
	}
	if len(s) != 16 || s[10] != 'T' {
		return time.Time{}, errors.New("Inspection time format must be YYYY-MM-DDTHH:MM")
	}
	d, err := time.Parse("2006-01-02T15:04", s)
	if err != nil || d.Year() < 2000 || d.Year() > 2100 {
		return time.Time{}, errors.New("Valid local inspection date and time required (2000-2100)")
	}
	return d, nil
}
func uuid() string {
	var b [16]byte
	_, err := rand.Read(b[:])
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Server) jobFile(id string) (string, error) {
	if !jobIDPattern.MatchString(id) {
		return "", errors.New("Invalid job identifier")
	}
	return filepath.Join(s.jobs, id+".json"), nil
}
func atomicWrite(p string, b []byte) error {
	tmp := p + "." + uuid() + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	e := f.Close()
	if err == nil {
		err = e
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	err = os.Rename(tmp, p)
	if err != nil && runtime.GOOS == "windows" { // Windows Rename cannot replace existing files; retain backup until replace succeeds.
		bak := p + ".backup"
		os.Remove(bak)
		if e := os.Rename(p, bak); e != nil && !os.IsNotExist(e) {
			os.Remove(tmp)
			return e
		}
		if e := os.Rename(tmp, p); e != nil {
			os.Rename(bak, p)
			os.Remove(tmp)
			return e
		}
		return nil
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
func jsonBytes(v any) []byte { b, _ := json.MarshalIndent(v, "", "  "); return b }
func (s *Server) load(id string) (Job, error) {
	var j Job
	p, err := s.jobFile(id)
	if err != nil {
		return j, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return j, errors.New("Job does not exist")
	}
	// v2 saved the price as an HTML input string. Preserve existing customer records.
	var raw map[string]json.RawMessage
	if err = json.Unmarshal(b, &raw); err != nil {
		return j, err
	}
	cost := raw["price"]
	delete(raw, "price")
	other, _ := json.Marshal(raw)
	if err = json.Unmarshal(other, &j); err != nil {
		return j, err
	}
	if len(cost) > 0 {
		if err = json.Unmarshal(cost, &j.Price); err != nil {
			var str string
			if json.Unmarshal(cost, &str) != nil {
				return j, errors.New("Invalid legacy price")
			}
			if str != "" {
				if j.Price, err = strconv.ParseFloat(str, 64); err != nil {
					return j, err
				}
			}
		}
	}
	return j, nil
}
func (s *Server) write(j Job) error {
	p, err := s.jobFile(j.ID)
	if err != nil {
		return err
	}
	if previous, readErr := os.ReadFile(p); readErr == nil {
		backupDir := filepath.Join(s.data, "Backups")
		if err := os.MkdirAll(backupDir, 0700); err != nil {
			return err
		}
		if err := atomicWrite(filepath.Join(backupDir, j.ID+".previous.json"), previous); err != nil {
			return err
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	j.Status = ""
	j.Needs = nil
	return atomicWrite(p, jsonBytes(j))
}
func (s *Server) log(id, action, detail string) {
	l := map[string]string{"time": time.Now().Format("2006-01-02T15:04:05"), "jobId": id, "action": action, "detail": detail}
	f, e := os.OpenFile(filepath.Join(s.data, "activity.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(l)
	f.Write(append(b, '\n'))
}
func (s *Server) allJobs() []Job {
	out := []Job{}
	names, e := os.ReadDir(s.jobs)
	if e != nil {
		return out
	}
	for _, x := range names {
		if x.IsDir() || !strings.HasSuffix(x.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(x.Name(), ".json")
		j, e := s.load(id)
		if e == nil && j.ID != "" {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, k int) bool {
		if out[i].InspectionDateTime == out[k].InspectionDateTime {
			return out[i].ID < out[k].ID
		}
		return out[i].InspectionDateTime < out[k].InspectionDateTime
	})
	return out
}
func (s *Server) workspace(j Job) (string, error) {
	if j.WorkspacePath != "" {
		return j.WorkspacePath, nil
	}
	d, e := date(j.InspectionDateTime)
	if e != nil {
		return "", e
	}
	st, e := street(j.Address)
	if e != nil {
		return "", e
	}
	return filepath.Join(s.root, d.Format("Jan")+". "+strconv.Itoa(d.Year()), st), nil
}
func (s *Server) needs(j Job) []string {
	n := []string{}
	if j.Cancelled || j.Archived {
		return n
	}
	if j.ClientName == "" {
		n = append(n, "Client name missing")
	}
	if j.ClientEmail == "" {
		n = append(n, "Client email missing")
	}
	if !j.AgreementSigned {
		n = append(n, "Agreement not signed (manual status)")
	}
	if !j.PaymentComplete {
		n = append(n, "Payment not complete (manual status)")
	}
	if j.WorkspacePrepared {
		w, e := s.workspace(j)
		if e != nil {
			return append(n, "Invalid workspace path")
		}
		if _, e := os.Stat(w); e != nil {
			n = append(n, "Prepared job workspace missing")
		} else {
			for _, key := range []string{"fourPoint", "windMit"} {
				if j.Services[key] {
					for _, fn := range forms[key] {
						if _, e := os.Stat(filepath.Join(w, "Ancillary Insurance Inspections", fn)); e != nil {
							n = append(n, fn+" missing")
						}
					}
				}
			}
		}
	}
	return n
}
func (s *Server) status(j Job) string {
	if j.Archived {
		return "Archived"
	}
	if j.Cancelled {
		return "Cancelled"
	}
	if j.Delivered {
		return "Delivered / Follow-Up"
	}
	if j.Inspected {
		return "Report / Docs Pending"
	}
	if j.WorkspacePrepared {
		if len(s.needs(j)) == 0 {
			return "Ready"
		}
		return "Preparing"
	}
	return "New"
}
func (s *Server) display(j Job) Job { j.Status = s.status(j); j.Needs = s.needs(j); return j }
func valueString(d map[string]json.RawMessage, key string) string {
	var v string
	json.Unmarshal(d[key], &v)
	return strings.TrimSpace(v)
}
func valueBool(d map[string]json.RawMessage, key string) bool {
	var b bool
	json.Unmarshal(d[key], &b)
	return b
}
func clean(d map[string]json.RawMessage) (Job, error) {
	j := Job{Services: map[string]bool{}}
	j.Address = valueString(d, "address")
	j.InspectionDateTime = valueString(d, "inspectionDateTime")
	if _, e := street(j.Address); e != nil {
		return j, e
	}
	if _, e := date(j.InspectionDateTime); e != nil {
		return j, e
	}
	for _, entry := range []struct {
		name   string
		target *string
	}{{"clientName", &j.ClientName}, {"clientEmail", &j.ClientEmail}, {"clientPhone", &j.ClientPhone}, {"agentName", &j.AgentName}, {"notes", &j.Notes}} {
		*entry.target = valueString(d, entry.name)
		cap := 300
		if entry.name == "notes" {
			cap = 3000
		}
		if len(*entry.target) > cap {
			return j, errors.New(entry.name + " too long")
		}
	}
	if len(j.Address) > 300 {
		return j, errors.New("address too long")
	}
	j.AgreementSigned = valueBool(d, "agreementSigned")
	j.PaymentComplete = valueBool(d, "paymentComplete")
	var sv map[string]bool
	json.Unmarshal(d["services"], &sv)
	selected := false
	for _, k := range services {
		j.Services[k] = sv[k]
		selected = selected || sv[k]
	}
	if !selected {
		return j, errors.New("Select at least one service")
	}
	if len(d["price"]) != 0 {
		var f float64
		if err := json.Unmarshal(d["price"], &f); err != nil {
			var str string
			if json.Unmarshal(d["price"], &str) != nil {
				return j, errors.New("Invalid price")
			}
			if str != "" {
				f, err = strconv.ParseFloat(str, 64)
				if err != nil {
					return j, errors.New("Invalid price")
				}
			}
		}
		if f < 0 || f > 1000000 || math.IsInf(f, 0) || math.IsNaN(f) {
			return j, errors.New("Price must be between $0 and $1,000,000")
		}
		j.Price = math.Round(f*100) / 100
	}
	return j, nil
}
func (s *Server) save(d map[string]json.RawMessage) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, e := clean(d)
	if e != nil {
		return c, e
	}
	id := valueString(d, "id")
	var old Job
	var existed bool
	if id != "" {
		old, e = s.load(id)
		if e != nil {
			return c, e
		}
		existed = true
	}
	if existed {
		var rev int
		json.Unmarshal(d["revision"], &rev)
		if rev != old.Revision {
			return c, errors.New("This job changed elsewhere. Reload it before saving.")
		}
		if old.WorkspacePrepared && (c.Address != old.Address || c.InspectionDateTime != old.InspectionDateTime) {
			return c, errors.New("Prepared job address/date is locked to protect its workspace. Create another job.")
		}
		if old.Cancelled || old.Archived {
			return c, errors.New("Inactive job cannot be edited")
		}
	} else {
		id = uuid()
	}
	old.ID = id
	old.Revision++
	old.Address = c.Address
	old.InspectionDateTime = c.InspectionDateTime
	old.ClientName = c.ClientName
	old.ClientEmail = c.ClientEmail
	old.ClientPhone = c.ClientPhone
	old.AgentName = c.AgentName
	old.Price = c.Price
	old.Notes = c.Notes
	old.Services = c.Services
	old.AgreementSigned = c.AgreementSigned
	old.PaymentComplete = c.PaymentComplete
	old.Updated = time.Now().Format("2006-01-02T15:04:05")
	if e := s.write(old); e != nil {
		return c, e
	}
	s.log(id, "SAVE", map[bool]string{true: "Job updated", false: "Job created"}[existed])
	return s.display(old), nil
}
func (s *Server) prepare(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.load(id)
	if e != nil {
		return "", e
	}
	if j.Archived || j.Cancelled {
		return "", errors.New("Cannot prepare inactive job")
	}
	w, e := s.workspace(j)
	if e != nil {
		return "", e
	}
	owner := filepath.Join(w, ".topnotch-job-id")
	if _, e := os.Stat(w); e == nil {
		b, e := os.ReadFile(owner)
		if e == nil && strings.TrimSpace(string(b)) != id {
			return "", errors.New("Workspace already belongs to another job at this address. No files changed.")
		}
		if os.IsNotExist(e) {
			if _, e2 := os.Stat(filepath.Join(w, "JobInfo.json")); e2 == nil {
				return "", errors.New("Existing job folder needs review before Office Manager can claim it. No files changed.")
			}
		}
	}
	for _, svc := range []string{"fourPoint", "windMit"} {
		if j.Services[svc] {
			for _, fn := range forms[svc] {
				if _, e := os.Stat(filepath.Join(s.base, "Templates", fn)); e != nil {
					return "", fmt.Errorf("Template missing: %s", fn)
				}
			}
		}
	}
	anc := filepath.Join(w, "Ancillary Insurance Inspections")
	for _, p := range []string{w, anc, filepath.Join(anc, "Backups"), filepath.Join(w, "Permits")} {
		if e := os.MkdirAll(p, 0700); e != nil {
			return "", e
		}
	}
	if _, e := os.Stat(owner); os.IsNotExist(e) {
		if e = os.WriteFile(owner, []byte(id), 0600); e != nil {
			return "", e
		}
	}
	jobInfo := filepath.Join(w, "JobInfo.json")
	if _, e := os.Stat(jobInfo); os.IsNotExist(e) {
		// CC v3.1.8 expects PascalCase service flags and a verified-address object.
		// Write a DRAFT only when no JobInfo exists. Do not clobber CC's records.
		info := map[string]any{
			"Version": "OfficeManager-Draft", "OfficeManagerJobId": id,
			"PropertyAddress": j.Address, "InspectionDateTime": j.InspectionDateTime + ":00",
			"Buyer":               map[string]string{"Name": j.ClientName, "Email": j.ClientEmail, "Phone": j.ClientPhone},
			"BuyerAgent":          map[string]string{"Name": j.AgentName},
			"AccessNotes":         j.Notes,
			"AddressVerification": map[string]any{"Verified": false, "NormalizedAddress": "", "Source": ""},
			"Services":            map[string]any{"FourPoint": j.Services["fourPoint"], "WindMit": j.Services["windMit"], "WDO": false, "WDOStatus": ""},
			"Notice":              "DRAFT. Independently verify address and all inspection answers in Command Center. Blank insurance templates are NOT completed reports.",
		}
		if e = atomicWrite(jobInfo, jsonBytes(info)); e != nil {
			return "", e
		}
	}
	for _, svc := range []string{"fourPoint", "windMit"} {
		if j.Services[svc] {
			for _, fn := range forms[svc] {
				dst := filepath.Join(anc, fn)
				if _, e := os.Stat(dst); os.IsNotExist(e) {
					src := filepath.Join(s.base, "Templates", fn)
					if e = copyFile(src, dst); e != nil {
						return "", e
					}
				}
			}
		}
	}
	j.WorkspacePath = w
	j.WorkspacePrepared = true
	j.Revision++
	if e := s.write(j); e != nil {
		return "", e
	}
	s.log(id, "PREPARE", w)
	return w, nil
}
func copyFile(src, dst string) error {
	in, e := os.Open(src)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	defer out.Close()
	_, e = io.Copy(out, in)
	return e
}
func (s *Server) transition(id, step string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, e := s.load(id)
	if e != nil {
		return j, e
	}
	if j.Archived || j.Cancelled {
		return j, errors.New("Inactive job cannot be changed")
	}
	switch step {
	case "inspected":
		j.Inspected = true
	case "delivered":
		if !j.Inspected {
			return j, errors.New("Mark inspected before delivered")
		}
		j.Delivered = true
	case "cancelled":
		j.Cancelled = true
	case "archived":
		j.Archived = true
	default:
		return j, errors.New("Unsupported transition")
	}
	j.Revision++
	if e := s.write(j); e != nil {
		return j, e
	}
	s.log(id, "STATUS", step)
	return s.display(j), nil
}
func (s *Server) state() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	js := s.allJobs()
	for i, j := range js {
		js[i] = s.display(j)
	}
	acts := []map[string]any{}
	if b, e := os.ReadFile(filepath.Join(s.data, "activity.jsonl")); e == nil {
		lines := strings.Split(strings.TrimSpace(string(b)), "\n")
		start := len(lines) - 150
		if start < 0 {
			start = 0
		}
		for _, line := range lines[start:] {
			var a map[string]any
			if json.Unmarshal([]byte(line), &a) == nil {
				acts = append(acts, a)
			}
		}
	}
	for i, k := 0, len(acts)-1; i < k; i, k = i+1, k-1 {
		acts[i], acts[k] = acts[k], acts[i]
	}
	return map[string]any{"jobs": js, "activity": acts, "root": s.root, "integrations": s.integrationState()}
}
func (s *Server) send(w http.ResponseWriter, status int, obj any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(obj)
}
func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	if r.Host != fmt.Sprintf("127.0.0.1:%d", s.port) && r.Host != fmt.Sprintf("localhost:%d", s.port) {
		s.send(w, 403, map[string]string{"error": "Invalid host"})
		return
	}
	if r.Method == "GET" {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'self'")
			w.Write(s.html)
		case "/api/state":
			s.send(w, 200, s.state())
		default:
			s.send(w, 404, map[string]string{"error": "Not found"})
		}
		return
	}
	if r.Method != "POST" {
		s.send(w, 405, map[string]string{"error": "Method not allowed"})
		return
	}
	if strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]) != "application/json" {
		s.send(w, 415, map[string]string{"error": "Only JSON requests accepted"})
		return
	}
	origin := r.Header.Get("Origin")
	if origin != "" && origin != fmt.Sprintf("http://127.0.0.1:%d", s.port) && origin != fmt.Sprintf("http://localhost:%d", s.port) {
		s.send(w, 403, map[string]string{"error": "Cross-site requests blocked"})
		return
	}
	if r.ContentLength <= 0 || r.ContentLength > 100000 {
		s.send(w, 413, map[string]string{"error": "Invalid request size"})
		return
	}
	var d map[string]json.RawMessage
	if e := json.NewDecoder(io.LimitReader(r.Body, 100001)).Decode(&d); e != nil {
		s.send(w, 400, map[string]string{"error": "Invalid JSON"})
		return
	}
	id := valueString(d, "id")
	switch r.URL.Path {
	case "/api/save":
		j, e := s.save(d)
		if e != nil {
			s.send(w, 400, map[string]string{"error": e.Error()})
		} else {
			s.send(w, 200, map[string]any{"job": j})
		}
	case "/api/prepare":
		p, e := s.prepare(id)
		if e != nil {
			s.send(w, 400, map[string]string{"error": e.Error()})
		} else {
			s.send(w, 200, map[string]string{"workspace": p})
		}
	case "/api/status":
		j, e := s.transition(id, valueString(d, "step"))
		if e != nil {
			s.send(w, 400, map[string]string{"error": e.Error()})
		} else {
			s.send(w, 200, map[string]any{"job": j})
		}
	case "/api/open":
		what := valueString(d, "what")
		var target, handoff string
		var e error
		if what == "cc" {
			cc := s.commandCenterHome()
			if !fileExists(filepath.Join(cc, "RUN_TOP_NOTCH_COMMAND_CENTER.bat")) ||
				!fileExists(filepath.Join(cc, "App", "TopNotch_CommandCenter.ps1")) ||
				!fileExists(filepath.Join(cc, "Scripts", "Launch.ps1")) {
				s.send(w, 400, map[string]string{"error": "Command Center is not installed completely"})
				return
			}
			target = filepath.Join(cc, "RUN_TOP_NOTCH_COMMAND_CENTER.bat")
			if id != "" {
				j, err := s.load(id)
				if err != nil {
					s.send(w, 400, map[string]string{"error": err.Error()})
					return
				}
				if !j.WorkspacePrepared {
					s.send(w, 400, map[string]string{"error": "Prepare this job before opening it in Command Center"})
					return
				}
				jobDir, err := s.workspace(j)
				if err != nil {
					s.send(w, 400, map[string]string{"error": err.Error()})
					return
				}
				handoff = filepath.Join(jobDir, "JobInfo.json")
				if !fileExists(handoff) {
					s.send(w, 400, map[string]string{"error": "Prepared JobInfo.json is missing"})
					return
				}
			}
		} else if what == "folder" {
			if id == "" {
				s.send(w, 400, map[string]string{"error": "Select a job first"})
				return
			}
			var j Job
			j, e = s.load(id)
			if e == nil {
				target, e = s.workspace(j)
			}
		} else {
			e = errors.New("Invalid launch target")
		}
		if e != nil {
			s.send(w, 400, map[string]string{"error": e.Error()})
			return
		}
		if _, e = os.Stat(target); e != nil {
			s.send(w, 400, map[string]string{"error": "Target does not exist"})
			return
		}
		if runtime.GOOS != "windows" {
			s.send(w, 400, map[string]string{"error": "Desktop launch requires Windows"})
			return
		}
		cmd := exec.Command("cmd.exe", "/d", "/c", "start", "", target)
		if handoff != "" {
			cmd.Env = append(os.Environ(), "TOPNOTCH_CC_JOBINFO="+handoff)
		}
		if e = cmd.Start(); e != nil {
			s.send(w, 400, map[string]string{"error": e.Error()})
		} else {
			s.send(w, 200, map[string]any{"ok": true, "jobHandoff": handoff != ""})
		}
	default:
		s.send(w, 404, map[string]string{"error": "Not found"})
	}
}
func main() {
	exe, e := os.Executable()
	if e != nil {
		log.Fatal(e)
	}
	base := filepath.Dir(exe)
	s, e := newServer(base)
	if e != nil {
		log.Fatal(e)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		log.Fatal(e)
	}
	s.port = ln.Addr().(*net.TCPAddr).Port
	url := fmt.Sprintf("http://127.0.0.1:%d/", s.port)
	fmt.Println("Top Notch Office Manager running at", url)
	if len(os.Args) < 2 || os.Args[1] != "--no-browser" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			if runtime.GOOS == "windows" {
				exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
			} else {
				exec.Command("xdg-open", url).Start()
			}
		}()
	}
	if e = http.Serve(ln, http.HandlerFunc(s.handler)); e != nil {
		log.Fatal(e)
	}
}
