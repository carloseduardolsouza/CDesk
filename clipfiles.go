package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// Área de transferência de arquivos: com a opção ligada no menu da sessão, arquivos copiados no
// Explorer de um computador (Ctrl+C) podem ser colados (Ctrl+V) no outro. Os arquivos são
// transferidos na hora para uma pasta temporária do outro lado e colocados na área de
// transferência dele como se tivessem sido copiados ali.
// shortcut: transfere já na cópia (não só ao colar), com limite de 256 MB e 5000 itens por cópia;
// subir para arquivos virtuais (IDataObject) se isso incomodar.

const (
	clipFilesCap   = 256 << 20
	clipFilesItems = 5000
)

var lastFileSeq atomic.Uint32 // última sequência da área de transferência já tratada

func clipTmpRoot() string { return filepath.Join(os.TempDir(), "cdesk-clip") }

// newClipTmp cria uma pasta temporária para arquivos que vieram do outro computador e apaga as antigas.
func newClipTmp() (string, error) {
	root := clipTmpRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if des, err := os.ReadDir(root); err == nil {
		for _, de := range des {
			if fi, err := de.Info(); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
				os.RemoveAll(filepath.Join(root, de.Name()))
			}
		}
	}
	return os.MkdirTemp(root, "c")
}

// inClipTmp diz se p está dentro da pasta temporária (arquivos colocados por nós, que não devem voltar).
func inClipTmp(p string) bool {
	root := strings.ToLower(clipTmpRoot()) + string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(filepath.Clean(p))+string(filepath.Separator), root)
}

type clipFilesMsg struct {
	Dir   string   `json:"dir"`
	Names []string `json:"names"`
}

// fileClipTick olha se o usuário copiou arquivos e, se a sessão ativa pede, manda para o outro lado.
func (a *App) fileClipTick() {
	a.mu.Lock()
	hc, id := a.in, a.activeView
	vs := a.out[id]
	a.mu.Unlock()
	hostOn := hc != nil && hc.fileClip.Load() && hc.clip.Load() && hc.ctl.Load() && hc.files.Load()
	viewOn := vs != nil && vs.fileClip.Load()
	if !hostOn && !viewOn {
		return
	}
	seq := clipSeq()
	if seq == lastFileSeq.Load() {
		return
	}
	lastFileSeq.Store(seq)
	paths := clipFiles()
	if len(paths) == 0 || inClipTmp(paths[0]) {
		return
	}
	dir := filepath.Dir(paths[0])
	var names []string
	for _, p := range paths {
		if filepath.Dir(p) == dir {
			names = append(names, filepath.Base(p))
		}
	}
	items, total, err := planLocal(dir, names)
	if err != nil || total > clipFilesCap || len(items) > clipFilesItems {
		a.emit("fileClip", id, "skip", "Os arquivos copiados são grandes demais para a área de transferência (limite: 256 MB). Use o gerenciador de arquivos.")
		return
	}
	if hostOn {
		b, _ := json.Marshal(clipFilesMsg{Dir: dir, Names: names})
		hc.sendMsg(msgClipFiles, b)
	}
	if viewOn {
		go a.pushFilesToHost(id, vs, dir, names)
	}
}

// pushFilesToHost envia os arquivos copiados aqui para uma pasta temporária do host e pede a ele que os cole na área de transferência dele.
func (a *App) pushFilesToHost(id string, v *viewSession, dir string, names []string) {
	resp, err := a.FilesOp(id, true, FileReq{Op: "cliptmp"})
	if err == nil {
		err = a.FilesUpload(id, "c"+id+time.Now().Format("150405.000"), dir, names, resp.Path)
	}
	if err != nil {
		a.emit("fileClip", id, "err", err.Error())
		return
	}
	b, _ := json.Marshal(clipFilesMsg{Dir: resp.Path, Names: names})
	line, _ := json.Marshal(Event{T: "clipfiles", S: string(b)})
	v.write(append(line, '\n'))
	a.emit("fileClip", id, "sent", "")
}

// hostSetClipFiles recebe do viewer a pasta temporária com os arquivos e os coloca na área de transferência deste computador.
func (a *App) hostSetClipFiles(c *hostConn, s string) {
	if !c.fileClip.Load() || !c.clip.Load() || !c.ctl.Load() || !c.files.Load() {
		return
	}
	var m clipFilesMsg
	if json.Unmarshal([]byte(s), &m) != nil || !inClipTmp(m.Dir) {
		return
	}
	var paths []string
	for _, n := range m.Names {
		p, err := safeJoin(m.Dir, n)
		if err != nil || !exists(p) {
			return
		}
		paths = append(paths, p)
	}
	if len(paths) > 0 && setClipFiles(paths) == nil {
		lastFileSeq.Store(clipSeq())
	}
}

// pullFilesFromHost baixa os arquivos que o host copiou e os põe na área de transferência deste computador.
func (a *App) pullFilesFromHost(id string, m clipFilesMsg) {
	tmp, err := newClipTmp()
	if err == nil {
		err = a.FilesDownload(id, "c"+id+time.Now().Format("150405.000"), m.Dir, m.Names, tmp)
	}
	if err == nil {
		var paths []string
		for _, n := range m.Names {
			if p, e := safeJoin(tmp, n); e == nil && exists(p) {
				paths = append(paths, p)
			}
		}
		if len(paths) == 0 {
			err = errors.New("nada foi baixado")
		} else if err = setClipFiles(paths); err == nil {
			lastFileSeq.Store(clipSeq())
		}
	}
	if err != nil {
		a.emit("fileClip", id, "err", err.Error())
		return
	}
	a.emit("fileClip", id, "ready", "")
}
