package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Verificação de atualização: pergunta ao GitHub qual é a última versão publicada do CDesk e
// compara com a que está rodando. Só avisa; não baixa nem instala nada.

const releasesURL = "https://api.github.com/repos/carloseduardolsouza/CDesk/releases/latest"

// UpdateInfo é o resultado da verificação.
type UpdateInfo struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	URL     string `json:"url"`
	Newer   bool   `json:"newer"` // há uma versão mais nova que a atual
}

// CheckUpdate procura a versão mais nova publicada.
func (a *App) CheckUpdate() (UpdateInfo, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", releasesURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "CDesk/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("Não foi possível consultar as atualizações. Confira a internet.")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return UpdateInfo{Current: version, Latest: version}, nil // nenhuma versão publicada ainda
	}
	var r struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&r) != nil || r.Tag == "" {
		return UpdateInfo{}, fmt.Errorf("Resposta inesperada do servidor de atualizações.")
	}
	return UpdateInfo{Current: version, Latest: strings.TrimPrefix(r.Tag, "v"), URL: r.URL, Newer: versionNewer(r.Tag, version)}, nil
}

// versionNewer diz se a é uma versão maior que b ("v1.2.0" e "1.2" valem). Parte não numérica vale 0.
func versionNewer(a, b string) bool {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.SplitN(pa[i], "-", 2)[0])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.SplitN(pb[i], "-", 2)[0])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
