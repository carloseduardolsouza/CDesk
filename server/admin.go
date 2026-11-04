package main

import (
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

//go:embed web/index.html
var indexHTML []byte

var idRe = regexp.MustCompile(`^\d{9}$`)

type session struct {
	csrf string
	exp  time.Time
}

type Admin struct {
	st    *Store
	g     *Guard
	d     *Dir
	srvID string
	mu    sync.Mutex
	sess  map[string]session
	fails map[string][]time.Time
}

func newAdmin(st *Store, g *Guard, d *Dir, srvID string) *Admin {
	return &Admin{st: st, g: g, d: d, srvID: srvID, sess: map[string]session{}, fails: map[string][]time.Time{}}
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// initAdmin cria o admin (usuário admin, senha admin123) na primeira execução e devolve a senha (vazia se já existia).
const defaultAdminPW = "admin123" // shortcut: senha fixa e conhecida; troque em Conta antes de expor o painel na internet.

func initAdmin(st *Store) (string, error) {
	if st.Get("admin_hash") != "" {
		return "", nil
	}
	return defaultAdminPW, setAdmin(st, defaultAdminPW)
}

// setAdmin define usuário admin e a senha, sem exigir troca.
func setAdmin(st *Store, pw string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	st.Set("admin_user", "admin")
	st.Set("must_change", "0")
	return st.Set("admin_hash", string(h))
}

func (a *Admin) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; script-src 'unsafe-inline'; frame-ancestors 'none'")
		w.Write(indexHTML)
	})
	m.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	m.HandleFunc("/api/login", a.login)
	m.HandleFunc("/api/logout", a.auth(a.logout))
	m.HandleFunc("/api/me", a.auth(a.me))
	m.HandleFunc("/api/live", a.auth(a.live))
	m.HandleFunc("/api/limits", a.auth(a.limits))
	m.HandleFunc("/api/acl", a.auth(a.acl))
	m.HandleFunc("/api/allowonly", a.auth(a.allowOnly))
	m.HandleFunc("/api/kick", a.auth(a.kick))
	m.HandleFunc("/api/usage", a.auth(a.usage))
	m.HandleFunc("/api/password", a.auth(a.password))
	return m
}

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func body(r *http.Request, v any) bool {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 16<<10)).Decode(v) == nil
}

func (a *Admin) session(r *http.Request) (string, session, bool) {
	c, err := r.Cookie("sid")
	if err != nil {
		return "", session{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sess[c.Value]
	if !ok || time.Now().After(s.exp) {
		delete(a.sess, c.Value)
		return "", session{}, false
	}
	return c.Value, s, true
}

// auth exige sessão; POST/PUT/DELETE exigem também o token CSRF; com a senha inicial só /api/password funciona.
func (a *Admin) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, s, ok := a.session(r)
		if !ok {
			fail(w, http.StatusUnauthorized, "login")
			return
		}
		if r.Method != http.MethodGet && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF")), []byte(s.csrf)) != 1 {
			fail(w, http.StatusForbidden, "csrf")
			return
		}
		if a.st.Get("must_change") == "1" && r.URL.Path != "/api/password" && r.URL.Path != "/api/me" && r.URL.Path != "/api/logout" {
			fail(w, http.StatusForbidden, "change-password")
			return
		}
		h(w, r)
	}
}

func (a *Admin) tooManyFails(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	keep := a.fails[ip][:0]
	for _, t := range a.fails[ip] {
		if time.Since(t) < 15*time.Minute {
			keep = append(keep, t)
		}
	}
	a.fails[ip] = keep
	return len(keep) >= 5
}

func (a *Admin) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "method")
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if a.tooManyFails(ip) {
		fail(w, http.StatusTooManyRequests, "Muitas tentativas. Aguarde 15 minutos.")
		return
	}
	var in struct{ User, Pass string }
	if !body(r, &in) {
		fail(w, http.StatusBadRequest, "json")
		return
	}
	okU := subtle.ConstantTimeCompare([]byte(in.User), []byte(a.st.Get("admin_user"))) == 1
	okP := bcrypt.CompareHashAndPassword([]byte(a.st.Get("admin_hash")), []byte(in.Pass)) == nil
	if !okU || !okP {
		a.mu.Lock()
		a.fails[ip] = append(a.fails[ip], time.Now())
		a.mu.Unlock()
		fail(w, http.StatusUnauthorized, "Usuário ou senha incorretos.")
		return
	}
	sid, s := randHex(32), session{csrf: randHex(16), exp: time.Now().Add(12 * time.Hour)}
	a.mu.Lock()
	a.sess[sid] = s
	a.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "sid", Value: sid, Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: 12 * 3600})
	reply(w, map[string]any{"csrf": s.csrf, "mustChange": a.st.Get("must_change") == "1"})
}

func (a *Admin) logout(w http.ResponseWriter, r *http.Request) {
	if sid, _, ok := a.session(r); ok {
		a.mu.Lock()
		delete(a.sess, sid)
		a.mu.Unlock()
	}
	reply(w, map[string]bool{"ok": true})
}

func (a *Admin) me(w http.ResponseWriter, r *http.Request) {
	_, s, _ := a.session(r)
	reply(w, map[string]any{"csrf": s.csrf, "mustChange": a.st.Get("must_change") == "1", "server": a.srvID})
}

// live manda o estado a cada segundo por SSE.
func (a *Admin) live(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, http.StatusInternalServerError, "sse")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		b, _ := json.Marshal(map[string]any{"directory": a.d.Online(), "peers": a.g.Live()})
		w.Write([]byte("data: "))
		w.Write(b)
		w.Write([]byte("\n\n"))
		fl.Flush()
		select {
		case <-t.C:
		case <-r.Context().Done():
			return
		}
	}
}

func validLimits(l Limits) bool {
	return l.MBps >= 0 && l.Circuits >= 0 && l.DayMB >= 0 && l.MonthMB >= 0
}

func (a *Admin) limits(w http.ResponseWriter, r *http.Request) {
	defer a.g.Reload() // o relay passa a usar os valores novos na hora
	switch r.Method {
	case http.MethodGet:
		m, err := a.st.Overrides()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		def := m[defaultID]
		delete(m, defaultID)
		reply(w, map[string]any{"default": def, "overrides": m})
	case http.MethodPut:
		var in struct {
			ID     string
			Limits Limits
		}
		if !body(r, &in) || (in.ID != defaultID && !idRe.MatchString(in.ID)) || !validLimits(in.Limits) {
			fail(w, http.StatusBadRequest, "Dados inválidos.")
			return
		}
		if err := a.st.SetLimit(in.ID, in.Limits); err != nil {
			fail(w, 500, err.Error())
			return
		}
		reply(w, map[string]bool{"ok": true})
	case http.MethodDelete:
		a.st.DelLimit(r.URL.Query().Get("id"))
		reply(w, map[string]bool{"ok": true})
	default:
		fail(w, http.StatusMethodNotAllowed, "method")
	}
}

func (a *Admin) acl(w http.ResponseWriter, r *http.Request) {
	defer a.g.Reload()
	switch r.Method {
	case http.MethodGet:
		es, err := a.st.Entries()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		reply(w, map[string]any{"entries": es, "allowOnly": a.st.Get("allowonly") == "1"})
	case http.MethodPost:
		var e Entry
		if !body(r, &e) || !idRe.MatchString(e.ID) || (e.Kind != "block" && e.Kind != "allow") || len(e.Note) > 200 {
			fail(w, http.StatusBadRequest, "Dados inválidos.")
			return
		}
		if err := a.st.AddEntry(e); err != nil {
			fail(w, 500, err.Error())
			return
		}
		if e.Kind == "block" {
			a.g.Kick(e.ID)
		}
		reply(w, map[string]bool{"ok": true})
	case http.MethodDelete:
		a.st.DelEntry(r.URL.Query().Get("id"))
		reply(w, map[string]bool{"ok": true})
	default:
		fail(w, http.StatusMethodNotAllowed, "method")
	}
}

func (a *Admin) allowOnly(w http.ResponseWriter, r *http.Request) {
	defer a.g.Reload()
	var in struct{ On bool }
	if r.Method != http.MethodPost || !body(r, &in) {
		fail(w, http.StatusBadRequest, "Dados inválidos.")
		return
	}
	v := "0"
	if in.On {
		v = "1"
	}
	a.st.Set("allowonly", v)
	reply(w, map[string]bool{"ok": true})
}

func (a *Admin) kick(w http.ResponseWriter, r *http.Request) {
	var in struct{ ID string }
	if r.Method != http.MethodPost || !body(r, &in) || !idRe.MatchString(in.ID) {
		fail(w, http.StatusBadRequest, "Dados inválidos.")
		return
	}
	a.g.Kick(in.ID)
	reply(w, map[string]bool{"ok": true})
}

func (a *Admin) usage(w http.ResponseWriter, _ *http.Request) {
	a.g.flush()
	day, month := a.g.keys()
	d, _ := a.st.TopSince(day, 20)
	m, _ := a.st.TopSince(month, 20)
	reply(w, map[string]any{"day": d, "month": m})
}

func (a *Admin) password(w http.ResponseWriter, r *http.Request) {
	var in struct{ Old, New string }
	if r.Method != http.MethodPost || !body(r, &in) {
		fail(w, http.StatusBadRequest, "Dados inválidos.")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(a.st.Get("admin_hash")), []byte(in.Old)) != nil {
		fail(w, http.StatusUnauthorized, "Senha atual incorreta.")
		return
	}
	if len(in.New) < 10 || len(in.New) > 72 { // bcrypt ignora além de 72 bytes
		fail(w, http.StatusBadRequest, "A nova senha precisa ter de 10 a 72 caracteres.")
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(in.New), bcrypt.DefaultCost)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	a.st.Set("admin_hash", string(h))
	a.st.Set("must_change", "0")
	reply(w, map[string]bool{"ok": true})
}
