package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
)

// Gerenciador de arquivos da sessão, como no AnyDesk: o viewer navega nas pastas dos dois
// computadores e envia/baixa arquivos e pastas, além das operações de um explorador (nova pasta,
// renomear, excluir, copiar, mover, abrir, propriedades). Cada operação abre um stream próprio
// (filesProto) na mesma conexão, então não atrapalha o vídeo da sessão.
// O host só atende o viewer da sessão em andamento e só se AllowControl e AllowFiles estiverem ligados.
// shortcut: excluir é definitivo (sem lixeira), não retoma transferência interrompida e quem já
// existe no destino ganha o nome "nome (2)" em vez de perguntar; subir quando pedirem "substituir".

const filesProto = "/cdesk-files/1.0.0"

const drivesPath = "drives" // caminho especial: lista os discos (Windows)

var errCanceled = errors.New(ErrCanceled)

// FileEntry é um item de uma pasta.
type FileEntry struct {
	Name string `json:"name"`
	Dir  bool   `json:"dir"`
	Size int64  `json:"size"`
	Mod  int64  `json:"mod"` // ms desde 1970
}

// FileList é o conteúdo de uma pasta; Path é o caminho já resolvido.
type FileList struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

// FileProps são as propriedades de um ou mais itens (pastas somam o conteúdo).
type FileProps struct {
	Size  int64 `json:"size"`
	Files int   `json:"files"`
	Dirs  int   `json:"dirs"`
	Mod   int64 `json:"mod"`
}

// FileReq é um pedido de arquivo. Op: list | mkdir | rename | delete | copy | move | open | props
// (resolvidos por fsOp) ou get | put (com dados, só pelo stream).
type FileReq struct {
	Op    string   `json:"op"`
	Path  string   `json:"path"`  // pasta de trabalho (absoluta)
	Name  string   `json:"name"`  // nome ou caminho relativo com "/" dentro de Path
	Names []string `json:"names"` // vários itens de Path (delete, copy, move, props)
	To    string   `json:"to"`    // rename: novo nome; copy/move: pasta de destino (absoluta)
	Size  int64    `json:"size"`
}

// FileResp é a resposta a um FileReq.
type FileResp struct {
	Err   string     `json:"err,omitempty"`
	Path  string     `json:"path,omitempty"` // cliptmp: pasta temporária criada
	Size  int64      `json:"size,omitempty"`
	List  *FileList  `json:"list,omitempty"`
	Props *FileProps `json:"props,omitempty"`
}

// listDir lista path; vazio = Documentos do usuário. Pastas primeiro, depois por nome.
func listDir(path string) (FileList, error) {
	if path == drivesPath && runtime.GOOS == "windows" {
		l := FileList{Path: drivesPath, Entries: []FileEntry{}}
		for c := 'A'; c <= 'Z'; c++ {
			if _, err := os.Stat(string(c) + `:\`); err == nil {
				l.Entries = append(l.Entries, FileEntry{Name: string(c) + `:\`, Dir: true})
			}
		}
		return l, nil
	}
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return FileList{}, err
		}
		path = home
		if d := filepath.Join(home, "Documents"); isDir(d) {
			path = d
		}
	}
	path = filepath.Clean(path)
	des, err := os.ReadDir(path)
	if err != nil {
		return FileList{}, err
	}
	l := FileList{Path: path, Entries: make([]FileEntry, 0, len(des))}
	for _, de := range des {
		fi, err := de.Info()
		if err != nil {
			continue // sumiu no meio da leitura ou sem permissão
		}
		e := FileEntry{Name: de.Name(), Dir: fi.IsDir(), Mod: fi.ModTime().UnixMilli()}
		if !e.Dir {
			e.Size = fi.Size()
		}
		l.Entries = append(l.Entries, e)
	}
	sort.Slice(l.Entries, func(i, j int) bool {
		a, b := l.Entries[i], l.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return l, nil
}

func isDir(p string) bool { fi, err := os.Stat(p); return err == nil && fi.IsDir() }

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// safeJoin junta dir (absoluto) e rel (nome ou caminho com "/"), recusando ".." e nomes estranhos.
func safeJoin(dir, rel string) (string, error) {
	if !filepath.IsAbs(dir) || rel == "" {
		return "", errors.New("Caminho inválido.")
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, `\:`) {
			return "", errors.New("Caminho inválido.")
		}
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

// uniquePath devolve p, ou "nome (2).ext", "nome (3).ext"... se p já existir.
func uniquePath(p string) string {
	if !exists(p) {
		return p
	}
	ext := filepath.Ext(p)
	base := strings.TrimSuffix(p, ext)
	for i := 2; ; i++ {
		if c := fmt.Sprintf("%s (%d)%s", base, i, ext); !exists(c) {
			return c
		}
	}
}

// createUnique cria o arquivo sem nunca sobrescrever um existente; devolve o caminho final.
func createUnique(path string) (*os.File, string, error) {
	for {
		p := uniquePath(path)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if !errors.Is(err, os.ErrExist) {
			return f, p, err
		}
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, p, err := createUnique(dst)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err == nil {
		err = out.Close()
	} else {
		out.Close()
	}
	if err != nil {
		os.Remove(p)
	}
	return err
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		t := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(t, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, t)
		}
		return nil // atalhos e arquivos especiais ficam de fora
	})
}

// fsOp executa as operações sem dados, no computador onde roda (local ou o host).
func fsOp(q FileReq) (FileResp, error) {
	names := q.Names
	if q.Name != "" && len(names) == 0 {
		names = []string{q.Name}
	}
	switch q.Op {
	case "list":
		l, err := listDir(q.Path)
		return FileResp{List: &l}, err
	case "mkdir":
		p, err := safeJoin(q.Path, q.Name)
		if err != nil {
			return FileResp{}, err
		}
		return FileResp{}, os.MkdirAll(p, 0o755)
	case "rename":
		src, err := safeJoin(q.Path, q.Name)
		if err != nil {
			return FileResp{}, err
		}
		if strings.ContainsAny(q.To, `/\`) {
			return FileResp{}, errors.New("Nome inválido.")
		}
		dst, err := safeJoin(q.Path, q.To)
		if err != nil {
			return FileResp{}, err
		}
		if exists(dst) {
			return FileResp{}, errors.New("Já existe um item com esse nome.")
		}
		return FileResp{}, os.Rename(src, dst)
	case "delete", "copy", "move", "props":
		var pr FileProps
		for _, n := range names {
			src, err := safeJoin(q.Path, n)
			if err != nil {
				return FileResp{}, err
			}
			switch q.Op {
			case "delete":
				err = os.RemoveAll(src)
			case "props":
				err = addProps(&pr, src)
			default:
				err = transfer(q.Op, src, q.To, n)
			}
			if err != nil {
				return FileResp{}, err
			}
		}
		if q.Op == "props" {
			return FileResp{Props: &pr}, nil
		}
		return FileResp{}, nil
	case "cliptmp": // pasta temporária para arquivos copiados vindos do outro computador
		p, err := newClipTmp()
		return FileResp{Path: p}, err
	case "open":
		p, err := safeJoin(q.Path, q.Name)
		if err != nil {
			return FileResp{}, err
		}
		return FileResp{}, exec.Command("rundll32", "url.dll,FileProtocolHandler", p).Start()
	}
	return FileResp{}, errors.New("Operação desconhecida.")
}

// transfer copia ou move src para a pasta toDir (mesmo computador).
func transfer(op, src, toDir, name string) error {
	if !filepath.IsAbs(toDir) {
		return errors.New("Pasta de destino inválida.")
	}
	dst := uniquePath(filepath.Join(toDir, filepath.Base(src)))
	if isDir(src) && (dst == src || strings.HasPrefix(dst, src+string(filepath.Separator))) {
		return errors.New("Não dá para colocar uma pasta dentro dela mesma.")
	}
	if op == "move" {
		if filepath.Dir(src) == filepath.Clean(toDir) {
			return nil // já está lá
		}
		if os.Rename(src, dst) == nil {
			return nil
		}
	}
	if isDir(src) {
		if err := copyTree(src, dst); err != nil {
			return err
		}
	} else if err := copyFile(src, dst); err != nil {
		return err
	}
	if op == "move" {
		return os.RemoveAll(src)
	}
	return nil
}

func addProps(pr *FileProps, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // sem permissão em um item: segue com os outros
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if p == root {
			pr.Mod = max(pr.Mod, fi.ModTime().UnixMilli())
		}
		if d.IsDir() {
			pr.Dirs++
		} else if d.Type().IsRegular() {
			pr.Files++
			pr.Size += fi.Size()
		}
		return nil
	})
}

// ---- lado host ----

func (a *App) serveFiles(s network.Stream) {
	defer s.Close()
	r := bufio.NewReader(s)
	reply := func(v FileResp) { b, _ := json.Marshal(v); s.Write(append(b, '\n')) }

	a.mu.Lock()
	in := a.in
	a.mu.Unlock()
	if in == nil || in.s.Conn().RemotePeer() != s.Conn().RemotePeer() || !in.ctl.Load() || !in.files.Load() {
		reply(FileResp{Err: "Transferência de arquivos não permitida neste computador."})
		return
	}
	s.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := r.ReadString('\n')
	if err != nil {
		return
	}
	s.SetReadDeadline(time.Time{})
	var q FileReq
	if json.Unmarshal([]byte(line), &q) != nil {
		return
	}
	switch q.Op {
	case "get":
		p, err := safeJoin(q.Path, q.Name)
		if err != nil {
			reply(FileResp{Err: err.Error()})
			return
		}
		f, err := os.Open(p)
		if err != nil {
			reply(FileResp{Err: err.Error()})
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil || fi.IsDir() {
			reply(FileResp{Err: "Só é possível baixar arquivos; pastas vão item por item."})
			return
		}
		reply(FileResp{Size: fi.Size()})
		if _, err := io.Copy(s, f); err == nil {
			a.hostLogAdd("Enviado: " + q.Name)
		}
	case "put":
		p, err := safeJoin(q.Path, q.Name)
		if err != nil || q.Size < 0 {
			reply(FileResp{Err: "Caminho inválido."})
			return
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		f, p, err := createUnique(p)
		if err != nil {
			reply(FileResp{Err: err.Error()})
			return
		}
		reply(FileResp{}) // pode enviar
		_, err = io.CopyN(f, r, q.Size)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(p) // não deixa arquivo pela metade
			return
		}
		a.hostLogAdd("Recebido: " + q.Name)
		reply(FileResp{})
	default:
		resp, err := fsOp(q)
		if err != nil {
			resp = FileResp{Err: err.Error()}
		}
		if err == nil {
			a.hostLogAdd(opLog(q))
		}
		reply(resp)
	}
}

// ---- lado viewer ----

// filesCall abre um stream de arquivos com o computador da sessão id e manda o pedido q.
func (a *App) filesCall(id string, q FileReq) (network.Stream, *bufio.Reader, FileResp, error) {
	a.mu.Lock()
	v := a.out[onlyDigits(id)]
	a.mu.Unlock()
	if v == nil {
		return nil, nil, FileResp{}, errors.New("Sessão encerrada.")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	s, err := a.h.NewStream(ctx, v.s.Conn().RemotePeer(), filesProto)
	if err != nil {
		return nil, nil, FileResp{}, err
	}
	b, _ := json.Marshal(q)
	r := bufio.NewReader(s)
	s.SetDeadline(time.Now().Add(60 * time.Second))
	var resp FileResp
	if _, err = s.Write(append(b, '\n')); err == nil {
		var l string
		if l, err = r.ReadString('\n'); err == nil {
			err = json.Unmarshal([]byte(l), &resp)
		}
	}
	if err == nil && resp.Err != "" {
		err = errors.New(resp.Err)
	}
	if err != nil {
		s.Reset()
		return nil, nil, FileResp{}, err
	}
	s.SetDeadline(time.Time{})
	return s, r, resp, nil
}

// FilesOp roda uma operação de pasta/arquivo neste computador (remote=false) ou no computador
// da sessão id (remote=true).
func (a *App) FilesOp(id string, remote bool, q FileReq) (FileResp, error) {
	if !remote {
		return fsOp(q)
	}
	s, _, resp, err := a.filesCall(id, q)
	if err != nil {
		return FileResp{}, err
	}
	s.Close()
	return resp, nil
}

// job é uma transferência em andamento (um arraste pode ter vários arquivos e pastas). Conta os
// bytes de tudo junto e avisa o frontend ("fileProgress") no máximo 5 vezes por segundo.
type job struct {
	a           *App
	id, key     string
	label       string
	done, total int64
	at          time.Time
	canceled    atomic.Bool
	mu          sync.Mutex
	cur         network.Stream // stream em uso, para o cancelamento interromper na hora
}

func (j *job) Write(b []byte) (int, error) {
	j.done += int64(len(b))
	j.emit(false)
	return len(b), nil
}

func (j *job) emit(force bool) {
	if force || time.Since(j.at) > 200*time.Millisecond {
		j.at = time.Now()
		j.a.emit("fileProgress", j.id, j.key, j.label, j.done, j.total)
	}
}

func (j *job) use(s network.Stream) { j.mu.Lock(); j.cur = s; j.mu.Unlock() }

// fail troca o erro de uma conexão derrubada pelo cancelamento, se foi isso que aconteceu.
func (j *job) fail(err error) error {
	if j.canceled.Load() {
		return errCanceled
	}
	return err
}

func (a *App) newJob(id, key string, names []string, total int64) *job {
	label := fmt.Sprintf("%d itens", len(names))
	if len(names) == 1 {
		label = names[0]
	}
	j := &job{a: a, id: id, key: key, label: label, total: total}
	a.mu.Lock()
	if a.jobs == nil {
		a.jobs = map[string]*job{}
	}
	a.jobs[key] = j
	a.mu.Unlock()
	j.emit(true)
	return j
}

func (a *App) endJob(key string) {
	a.mu.Lock()
	delete(a.jobs, key)
	a.mu.Unlock()
}

// FilesCancel interrompe a transferência key.
func (a *App) FilesCancel(key string) {
	a.mu.Lock()
	j := a.jobs[key]
	a.mu.Unlock()
	if j != nil {
		j.canceled.Store(true)
		j.mu.Lock()
		if j.cur != nil {
			j.cur.Reset()
		}
		j.mu.Unlock()
	}
}

type planItem struct {
	rel  string // caminho relativo com "/"
	dir  bool
	size int64
}

// planLocal lista tudo que será enviado: cada pasta antes do seu conteúdo.
func planLocal(dir string, names []string) (items []planItem, total int64, err error) {
	for _, n := range names {
		root, err := safeJoin(dir, n)
		if err != nil {
			return nil, 0, err
		}
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			switch {
			case d.IsDir():
				items = append(items, planItem{rel: rel, dir: true})
			case d.Type().IsRegular():
				fi, err := d.Info()
				if err != nil {
					return err
				}
				items = append(items, planItem{rel: rel, size: fi.Size()})
				total += fi.Size()
			}
			return nil
		})
		if err != nil {
			return nil, 0, err
		}
	}
	return items, total, nil
}

// remoteJoin junta pasta remota e caminho relativo com o separador do computador remoto.
func remoteJoin(dir, rel string) string {
	if sep := "/"; !strings.Contains(dir, `\`) {
		return strings.TrimRight(dir, sep) + sep + rel
	}
	return strings.TrimRight(dir, `\`) + `\` + strings.ReplaceAll(rel, "/", `\`)
}

func (a *App) planRemote(id, dir string, names []string) (items []planItem, total int64, err error) {
	var walk func(rel string) error
	walk = func(rel string) error {
		_, _, resp, err := a.filesCall(id, FileReq{Op: "list", Path: remoteJoin(dir, rel)})
		if err != nil {
			return err
		}
		for _, e := range resp.List.Entries {
			r := rel + "/" + e.Name
			if e.Dir {
				items = append(items, planItem{rel: r, dir: true})
				if err := walk(r); err != nil {
					return err
				}
			} else {
				items = append(items, planItem{rel: r, size: e.Size})
				total += e.Size
			}
		}
		return nil
	}
	_, _, top, err := a.filesCall(id, FileReq{Op: "list", Path: dir})
	if err != nil {
		return nil, 0, err
	}
	for _, n := range names {
		i := slicesIndex(top.List.Entries, n)
		if i < 0 {
			return nil, 0, errors.New("Item não encontrado: " + n)
		}
		e := top.List.Entries[i]
		if e.Dir {
			items = append(items, planItem{rel: n, dir: true})
			if err := walk(n); err != nil {
				return nil, 0, err
			}
		} else {
			items = append(items, planItem{rel: n, size: e.Size})
			total += e.Size
		}
	}
	return items, total, nil
}

func slicesIndex(es []FileEntry, name string) int {
	for i, e := range es {
		if e.Name == name {
			return i
		}
	}
	return -1
}

// FilesUpload envia arquivos e pastas (names, dentro de localDir) para remoteDir no computador da sessão id.
func (a *App) FilesUpload(id, key, localDir string, names []string, remoteDir string) error {
	items, total, err := planLocal(localDir, names)
	if err != nil {
		return err
	}
	j := a.newJob(id, key, names, total)
	defer a.endJob(key)
	for _, it := range items {
		if j.canceled.Load() {
			return errCanceled
		}
		if it.dir {
			if _, err := a.FilesOp(id, true, FileReq{Op: "mkdir", Path: remoteDir, Name: it.rel}); err != nil {
				return err
			}
		} else if err := a.uploadFile(j, localDir, it, remoteDir); err != nil {
			return j.fail(err)
		}
	}
	j.emit(true)
	return nil
}

func (a *App) uploadFile(j *job, localDir string, it planItem, remoteDir string) error {
	f, err := os.Open(filepath.Join(localDir, filepath.FromSlash(it.rel)))
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	s, r, _, err := a.filesCall(j.id, FileReq{Op: "put", Path: remoteDir, Name: it.rel, Size: fi.Size()})
	if err != nil {
		return err
	}
	defer s.Close()
	j.use(s)
	if _, err := io.Copy(s, io.TeeReader(f, j)); err != nil {
		s.Reset()
		return err
	}
	s.SetReadDeadline(time.Now().Add(60 * time.Second))
	var resp FileResp
	l, err := r.ReadString('\n') // o host confirma que gravou tudo
	if err == nil {
		err = json.Unmarshal([]byte(l), &resp)
	}
	if err == nil && resp.Err != "" {
		err = errors.New(resp.Err)
	}
	if err != nil {
		return fmt.Errorf("O envio não foi concluído: %w", err)
	}
	return nil
}

// FilesDownload baixa arquivos e pastas (names, dentro de remoteDir) do computador da sessão id para localDir.
func (a *App) FilesDownload(id, key, remoteDir string, names []string, localDir string) error {
	items, total, err := a.planRemote(id, remoteDir, names)
	if err != nil {
		return err
	}
	j := a.newJob(id, key, names, total)
	defer a.endJob(key)
	for _, it := range items {
		if j.canceled.Load() {
			return errCanceled
		}
		if it.dir {
			p, err := safeJoin(localDir, it.rel)
			if err == nil {
				err = os.MkdirAll(p, 0o755)
			}
			if err != nil {
				return err
			}
		} else if err := a.downloadFile(j, remoteDir, it, localDir); err != nil {
			return j.fail(err)
		}
	}
	j.emit(true)
	return nil
}

func (a *App) downloadFile(j *job, remoteDir string, it planItem, localDir string) error {
	dst, err := safeJoin(localDir, it.rel)
	if err != nil {
		return err
	}
	s, r, resp, err := a.filesCall(j.id, FileReq{Op: "get", Path: remoteDir, Name: it.rel})
	if err != nil {
		return err
	}
	defer s.Close()
	j.use(s)
	f, p, err := createUnique(dst)
	if err != nil {
		s.Reset()
		return err
	}
	_, err = io.CopyN(io.MultiWriter(f, j), r, resp.Size)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(p)
		return fmt.Errorf("O download não foi concluído: %w", err)
	}
	return nil
}

// opLog descreve, para a janela da sessão no host, o que o viewer fez; vazio = não vale registrar.
func opLog(q FileReq) string {
	what := q.Name
	if len(q.Names) > 0 {
		what = strings.Join(q.Names, ", ")
	}
	switch q.Op {
	case "mkdir":
		return "Pasta criada: " + what
	case "rename":
		return "Renomeado: " + what + " → " + q.To
	case "delete":
		return "Excluído: " + what
	case "copy":
		return "Copiado: " + what
	case "move":
		return "Movido: " + what
	case "open":
		return "Aberto: " + what
	}
	return ""
}
