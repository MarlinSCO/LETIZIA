package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Item struct {
	Name    string             `json:"name"`
	Opening float64            `json:"opening"`
	Load    float64            `json:"load"`
	Unloads map[string]float64 `json:"unloads"`
}

type Month struct {
	Key          string `json:"key"`
	Items        []Item `json:"items"`
	ArchivedAt   string `json:"archivedAt,omitempty"`
	ArchiveDirty bool   `json:"archiveDirty,omitempty"`
}

type ArchiveRow struct {
	Name    string  `json:"name"`
	Opening float64 `json:"opening"`
	Load    float64 `json:"load"`
	Unload  float64 `json:"unload"`
	Closing float64 `json:"closing"`
}

type Archive struct {
	Month      string       `json:"month"`
	ArchivedAt string       `json:"archivedAt"`
	Rows       []ArchiveRow `json:"rows"`
}

type Data struct {
	Version      int                 `json:"version"`
	CurrentMonth string              `json:"currentMonth"`
	Months       map[string]*Month   `json:"months"`
	Archives     map[string]*Archive `json:"archives"`
}

var (
	mu      sync.Mutex
	appData *Data
	saveTo  string
	server  *http.Server
)

func main() {
	var err error
	saveTo, err = portableDataPath()
	if err != nil {
		log.Fatal(err)
	}
	appData, err = loadData(saveTo)
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", pageHandler)
	mux.HandleFunc("/api/state", stateHandler)
	mux.HandleFunc("/api/switch", switchHandler)
	mux.HandleFunc("/api/update", updateHandler)
	mux.HandleFunc("/api/add", addHandler)
	mux.HandleFunc("/api/remove", removeHandler)
	mux.HandleFunc("/api/archive", archiveHandler)
	mux.HandleFunc("/api/archives", archivesHandler)
	mux.HandleFunc("/api/save", saveHandler)
	mux.HandleFunc("/api/backup", backupHandler)
	mux.HandleFunc("/api/shutdown", shutdownHandler)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	addr := "http://" + ln.Addr().String()
	server = &http.Server{Handler: mux}

	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = openBrowser(addr)
	}()

	if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func portableDataPath() (string, error) {
	// I dati sono separati dall'eseguibile. Questo permette al programma
	// installato tramite .deb (in /usr/bin) di salvare senza privilegi root
	// e consente di aggiornare/reinstallare l'app senza perdere il magazzino.
	var base string
	if runtime.GOOS == "linux" {
		if xdg := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); xdg != "" {
			base = xdg
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			base = filepath.Join(home, ".local", "share")
		}
	} else {
		dir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		base = dir
	}

	dir := filepath.Join(base, "MagazzinoPortatile")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "MagazzinoDati.json"), nil
}

func loadData(path string) (*Data, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		var d Data
		if e := json.Unmarshal(b, &d); e == nil && d.Months != nil {
			if d.Archives == nil {
				d.Archives = map[string]*Archive{}
			}
			return &d, nil
		}
	}
	d := emptyData()
	if err := persist(path, d); err != nil {
		return nil, err
	}
	return d, nil
}

func persist(path string, d *Data) error {
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func openingPlusLoad(i Item) float64 { return i.Opening + i.Load }
func unloadTotal(i Item) float64 {
	var t float64
	for _, v := range i.Unloads {
		t += v
	}
	return t
}
func closing(i Item) float64 { return i.Opening + i.Load - unloadTotal(i) }

func parseMonth(s string) (time.Time, error) { return time.Parse("2006-01", s) }
func nextMonth(t time.Time) time.Time        { return t.AddDate(0, 1, 0) }

func orderedMonthKeys(d *Data) []string {
	keys := make([]string, 0, len(d.Months))
	for k := range d.Months {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func ensureMonthLocked(target string) error {
	if _, err := parseMonth(target); err != nil {
		return fmt.Errorf("mese non valido")
	}
	if _, ok := appData.Months[target]; ok {
		appData.CurrentMonth = target
		return persist(saveTo, appData)
	}
	keys := orderedMonthKeys(appData)
	if len(keys) == 0 {
		return fmt.Errorf("nessun mese disponibile")
	}

	// Find the latest existing month before target.
	prevKey := ""
	for _, k := range keys {
		if k < target {
			prevKey = k
		}
	}
	if prevKey == "" {
		return fmt.Errorf("non esistono dati precedenti per %s", target)
	}

	prevT, _ := parseMonth(prevKey)
	targetT, _ := parseMonth(target)
	for t := nextMonth(prevT); !t.After(targetT); t = nextMonth(t) {
		k := t.Format("2006-01")
		if _, exists := appData.Months[k]; exists {
			prevKey = k
			continue
		}
		prev := appData.Months[prevKey]
		nm := &Month{Key: k, Items: make([]Item, 0, len(prev.Items))}
		for _, p := range prev.Items {
			nm.Items = append(nm.Items, Item{Name: p.Name, Opening: closing(p), Load: 0, Unloads: map[string]float64{}})
		}
		appData.Months[k] = nm
		prevKey = k
	}
	appData.CurrentMonth = target
	return persist(saveTo, appData)
}

func propagateLocked(from string) {
	keys := orderedMonthKeys(appData)
	prevKey := from
	started := false
	for _, k := range keys {
		if k == from {
			started = true
			continue
		}
		if !started || k < from {
			continue
		}
		prev := appData.Months[prevKey]
		cur := appData.Months[k]
		prevMap := map[string]Item{}
		for _, p := range prev.Items {
			prevMap[p.Name] = p
		}
		curMap := map[string]int{}
		for idx := range cur.Items {
			curMap[cur.Items[idx].Name] = idx
		}
		for name, p := range prevMap {
			if idx, ok := curMap[name]; ok {
				cur.Items[idx].Opening = closing(p)
			} else {
				cur.Items = append(cur.Items, Item{Name: name, Opening: closing(p), Unloads: map[string]float64{}})
			}
		}
		sort.SliceStable(cur.Items, func(i, j int) bool { return strings.ToUpper(cur.Items[i].Name) < strings.ToUpper(cur.Items[j].Name) })
		if cur.ArchivedAt != "" {
			cur.ArchiveDirty = true
		}
		prevKey = k
	}
}

func mondayThursdayDates(key string) []string {
	t, _ := parseMonth(key)
	y, m, _ := t.Date()
	var out []string
	for d := 1; d <= 31; d++ {
		dt := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
		if dt.Month() != m {
			break
		}
		if dt.Weekday() == time.Monday || dt.Weekday() == time.Thursday {
			out = append(out, dt.Format("2006-01-02"))
		}
	}
	return out
}

func statePayloadLocked() map[string]any {
	m := appData.Months[appData.CurrentMonth]
	keys := orderedMonthKeys(appData)
	minMonth := keys[0]
	return map[string]any{
		"currentMonth": appData.CurrentMonth,
		"minMonth":     minMonth,
		"dates":        mondayThursdayDates(appData.CurrentMonth),
		"month":        m,
		"dataFile":     filepath.Base(saveTo),
	}
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func errOut(w http.ResponseWriter, status int, msg string) {
	jsonOut(w, status, map[string]any{"error": msg})
}
func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func stateHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	jsonOut(w, 200, statePayloadLocked())
}

func switchHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Month string `json:"month"`
	}
	if decodeJSON(r, &req) != nil {
		errOut(w, 400, "richiesta non valida")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if err := ensureMonthLocked(req.Month); err != nil {
		errOut(w, 400, err.Error())
		return
	}
	jsonOut(w, 200, statePayloadLocked())
}

func updateHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Month string  `json:"month"`
		Name  string  `json:"name"`
		Field string  `json:"field"`
		Date  string  `json:"date"`
		Value float64 `json:"value"`
	}
	if decodeJSON(r, &req) != nil {
		errOut(w, 400, "richiesta non valida")
		return
	}
	if req.Value < 0 {
		errOut(w, 400, "il valore non può essere negativo")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	m := appData.Months[req.Month]
	if m == nil {
		errOut(w, 404, "mese non trovato")
		return
	}
	found := false
	for idx := range m.Items {
		if m.Items[idx].Name == req.Name {
			found = true
			switch req.Field {
			case "opening":
				m.Items[idx].Opening = req.Value
			case "load":
				m.Items[idx].Load = req.Value
			case "unload":
				if m.Items[idx].Unloads == nil {
					m.Items[idx].Unloads = map[string]float64{}
				}
				if req.Value == 0 {
					delete(m.Items[idx].Unloads, req.Date)
				} else {
					m.Items[idx].Unloads[req.Date] = req.Value
				}
			default:
				errOut(w, 400, "campo non valido")
				return
			}
			break
		}
	}
	if !found {
		errOut(w, 404, "articolo non trovato")
		return
	}
	if m.ArchivedAt != "" {
		m.ArchiveDirty = true
	}
	propagateLocked(req.Month)
	if err := persist(saveTo, appData); err != nil {
		errOut(w, 500, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}

func addHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Month, Name string
		Opening     float64
	}
	if decodeJSON(r, &req) != nil {
		errOut(w, 400, "richiesta non valida")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		errOut(w, 400, "scrivi il nome dell'articolo")
		return
	}
	if req.Opening < 0 {
		errOut(w, 400, "la giacenza non può essere negativa")
		return
	}
	name = strings.ToUpper(name)
	mu.Lock()
	defer mu.Unlock()
	keys := orderedMonthKeys(appData)
	for _, k := range keys {
		if k < req.Month {
			continue
		}
		m := appData.Months[k]
		for _, it := range m.Items {
			if strings.EqualFold(it.Name, name) {
				errOut(w, 409, "l'articolo esiste già")
				return
			}
		}
	}
	cur := appData.Months[req.Month]
	if cur == nil {
		errOut(w, 404, "mese non trovato")
		return
	}
	cur.Items = append(cur.Items, Item{Name: name, Opening: req.Opening, Unloads: map[string]float64{}})
	sort.SliceStable(cur.Items, func(i, j int) bool { return cur.Items[i].Name < cur.Items[j].Name })
	if cur.ArchivedAt != "" {
		cur.ArchiveDirty = true
	}
	propagateLocked(req.Month)
	if err := persist(saveTo, appData); err != nil {
		errOut(w, 500, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}

func removeHandler(w http.ResponseWriter, r *http.Request) {
	var req struct{ Month, Name string }
	if decodeJSON(r, &req) != nil {
		errOut(w, 400, "richiesta non valida")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	keys := orderedMonthKeys(appData)
	removed := false
	for _, k := range keys {
		if k < req.Month {
			continue
		}
		m := appData.Months[k]
		out := m.Items[:0]
		for _, it := range m.Items {
			if it.Name == req.Name {
				removed = true
				continue
			}
			out = append(out, it)
		}
		m.Items = out
		if m.ArchivedAt != "" {
			m.ArchiveDirty = true
		}
	}
	if !removed {
		errOut(w, 404, "articolo non trovato")
		return
	}
	if err := persist(saveTo, appData); err != nil {
		errOut(w, 500, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}

func archiveHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Month     string `json:"month"`
		Overwrite bool   `json:"overwrite"`
	}
	if decodeJSON(r, &req) != nil {
		errOut(w, 400, "richiesta non valida")
		return
	}
	mu.Lock()
	defer mu.Unlock()
	m := appData.Months[req.Month]
	if m == nil {
		errOut(w, 404, "mese non trovato")
		return
	}
	if _, exists := appData.Archives[req.Month]; exists && !req.Overwrite {
		errOut(w, 409, "Il mese è già archiviato. Vuoi sostituire l'archivio esistente?")
		return
	}
	rows := make([]ArchiveRow, 0, len(m.Items))
	for _, it := range m.Items {
		rows = append(rows, ArchiveRow{Name: it.Name, Opening: it.Opening, Load: it.Load, Unload: unloadTotal(it), Closing: closing(it)})
	}
	stamp := time.Now().Format("02/01/2006 15:04")
	appData.Archives[req.Month] = &Archive{Month: req.Month, ArchivedAt: stamp, Rows: rows}
	m.ArchivedAt = stamp
	m.ArchiveDirty = false
	if err := persist(saveTo, appData); err != nil {
		errOut(w, 500, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "archivedAt": stamp})
}

func archivesHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	keys := make([]string, 0, len(appData.Archives))
	for k := range appData.Archives {
		keys = append(keys, k)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	arr := make([]*Archive, 0, len(keys))
	for _, k := range keys {
		arr = append(arr, appData.Archives[k])
	}
	jsonOut(w, 200, arr)
}

func saveHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	if err := persist(saveTo, appData); err != nil {
		errOut(w, 500, err.Error())
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true, "file": filepath.Base(saveTo)})
}

func backupHandler(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	defer mu.Unlock()
	b, err := json.MarshalIndent(appData, "", "  ")
	if err != nil {
		errOut(w, 500, err.Error())
		return
	}
	name := "Backup_Magazzino_" + time.Now().Format("20060102_150405") + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(b)
}

func shutdownHandler(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]any{"ok": true})
	go func() {
		time.Sleep(250 * time.Millisecond)
		if server != nil {
			_ = server.Close()
		}
		os.Exit(0)
	}()
}

func pageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, pageHTML)
}

func openBrowser(url string) error {
	var commands [][]string
	switch runtime.GOOS {
	case "windows":
		commands = [][]string{
			{"rundll32", "url.dll,FileProtocolHandler", url},
			{"cmd", "/c", "start", "", url},
		}
	case "linux":
		commands = [][]string{
			{"xdg-open", url},
			{"gio", "open", url},
		}
	case "darwin":
		commands = [][]string{{"open", url}}
	default:
		return fmt.Errorf("apri manualmente %s nel browser", url)
	}
	for _, c := range commands {
		if err := exec.Command(c[0], c[1:]...).Start(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("impossibile aprire automaticamente il browser: %s", url)
}

func emptyData() *Data {
	// Installazione pulita: nessun articolo, nessuna giacenza,
	// nessun movimento e nessun archivio precaricato.
	// Viene creato solo il contenitore strutturale del mese corrente.
	current := time.Now().Format("2006-01")
	return &Data{
		Version:      1,
		CurrentMonth: current,
		Months: map[string]*Month{
			current: {
				Key:   current,
				Items: []Item{},
			},
		},
		Archives: map[string]*Archive{},
	}
}

var _ = template.HTMLEscapeString
var _ = strconv.Itoa

const pageHTML = `<!doctype html>
<html lang="it">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Letizia</title>
<style>
:root{--blue:#17365d;--blue2:#d9eaf7;--yellow:#fff2cc;--green:#e2f0d9;--orange:#fce4d6;--line:#8ea9c1;--panel:#f5f7fa;--danger:#9c1c1c;--ink:#17202a}
*{box-sizing:border-box} body{margin:0;font-family:Segoe UI,Arial,sans-serif;color:var(--ink);background:#eef2f6}
header{background:var(--blue);color:white;padding:14px 20px;display:flex;align-items:center;justify-content:space-between;gap:16px}
header h1{font-size:20px;margin:0;letter-spacing:.3px} header .sub{font-size:12px;opacity:.85}
.app{padding:14px;display:grid;grid-template-columns:minmax(850px,1fr) 260px;gap:14px;max-width:1800px;margin:auto}
.card{background:white;border:1px solid #cbd5df;border-radius:8px;box-shadow:0 1px 3px rgba(0,0,0,.08)}
.toolbar{padding:12px;display:flex;align-items:center;gap:10px;flex-wrap:wrap;border-bottom:1px solid #d9e1e8}
.monthbox{display:flex;align-items:center;gap:8px;font-weight:700}.monthbox input{background:var(--yellow);border:1px solid #caa700;border-radius:5px;padding:8px 10px;font-weight:700}
.badge{font-size:12px;border-radius:999px;padding:4px 9px;background:#edf2f7;color:#374151}.badge.warn{background:#fff3cd;color:#7a5a00}.badge.ok{background:#e7f6e7;color:#256029}
.tablewrap{overflow:auto;max-height:calc(100vh - 170px)}table{border-collapse:collapse;min-width:100%;font-size:13px}th,td{border:1px solid var(--line);padding:5px;text-align:center;white-space:nowrap}th{position:sticky;top:0;z-index:3;background:#d9e2f3;font-weight:700}th.article,td.article{text-align:left;min-width:140px}th.opening,td.opening{background:#f2f2f2;min-width:78px}th.day,td.day{background:var(--blue2);min-width:67px}th.load,td.load{background:var(--green);min-width:72px}th.closing,td.closing{background:var(--orange);min-width:110px}th.total,td.total{background:#e7e6e6;min-width:72px}td input{width:62px;border:1px solid transparent;background:transparent;text-align:center;padding:5px;border-radius:3px}td input:focus{outline:2px solid #3b82f6;background:white}td.article{font-weight:600;background:#fff}tfoot td{font-weight:800;background:#d9e2f3;position:sticky;bottom:0;z-index:2}
.side{display:flex;flex-direction:column;gap:12px}.side section{padding:12px}.side h2{font-size:14px;margin:0 0 10px;color:var(--blue);border-bottom:2px solid var(--blue);padding-bottom:6px}.kpi{display:grid;grid-template-columns:1fr auto;gap:6px;font-size:13px;padding:5px 0;border-bottom:1px solid #edf0f2}.kpi b{font-size:14px}
button{border:1px solid #7d8da0;background:#fff;border-radius:5px;padding:8px 10px;cursor:pointer;font-weight:600}button:hover{background:#f0f4f8}.primary{background:var(--blue);color:#fff;border-color:var(--blue)}.primary:hover{background:#244b7d}.green{background:#e2f0d9;border-color:#8db36b}.danger{background:#fff0f0;border-color:#d89b9b;color:var(--danger)}.side button{width:100%;margin:4px 0;text-align:left}.status{position:fixed;right:18px;bottom:18px;background:#1f2937;color:#fff;padding:10px 14px;border-radius:7px;box-shadow:0 2px 8px #0004;display:none;z-index:30}
.dialog{position:fixed;inset:0;background:#0006;display:none;align-items:center;justify-content:center;z-index:20}.dialog.open{display:flex}.dialog .box{width:min(720px,92vw);max-height:80vh;overflow:auto;background:#fff;border-radius:9px;padding:18px;box-shadow:0 10px 40px #0006}.dialog h3{margin-top:0}.formrow{display:flex;gap:8px;align-items:center;margin:8px 0}.formrow input{padding:8px;border:1px solid #aaa;border-radius:4px;flex:1}.archive-card{border:1px solid #ccd4dd;border-radius:6px;padding:10px;margin:8px 0}.archive-card summary{cursor:pointer;font-weight:700}.archive-card table{margin-top:8px;font-size:12px}.right{text-align:right}
@media(max-width:1100px){.app{grid-template-columns:1fr}.tablewrap{max-height:none}.side{display:grid;grid-template-columns:1fr 1fr}.side section:last-child{grid-column:1/-1}}
@media print{body{background:white}.app{display:block;padding:0}.side,.toolbar,header .sub,.status,.dialog{display:none!important}header{background:white;color:black;padding:0 0 8px}header h1{font-size:16px}.card{border:none;box-shadow:none}.tablewrap{overflow:visible;max-height:none}table{font-size:9px;width:100%}th{position:static}tfoot td{position:static}th,td{padding:3px}@page{size:A4 landscape;margin:8mm}}
</style>
</head><body>
<header><div><h1>LETIZIA</h1><div class="sub">Gestione magazzino mensile — dati salvati automaticamente sul computer</div></div><div id="saveHint" class="sub"></div></header>
<div class="app">
  <main class="card">
    <div class="toolbar">
      <div class="monthbox">MESE DI LAVORO <input id="monthPicker" type="month"></div>
      <span id="archiveBadge" class="badge"></span>
      <button onclick="reloadState()">Aggiorna</button>
    </div>
    <div class="tablewrap"><table id="grid"></table></div>
  </main>
  <aside class="side">
    <section class="card"><h2>RIEPILOGO</h2><div id="summary"></div></section>
    <section class="card"><h2>GESTIONE ARTICOLI</h2><button class="green" onclick="openAdd()">＋ Aggiungi articolo</button><button class="danger" onclick="openRemove()">− Rimuovi articolo</button></section>
    <section class="card"><h2>OPERAZIONI MESE</h2><button class="primary" onclick="archiveMonth(false)">▣ Archivia mese</button><button onclick="window.print()">🖨 Anteprima / Stampa</button><button onclick="saveNow()">💾 Salva dati</button><button onclick="backup()">⬇ Esporta backup</button><button onclick="showArchives()">▤ Storico mensile</button><button class="danger" onclick="quitApp()">✕ Esci dal programma</button></section>
  </aside>
</div>
<div id="toast" class="status"></div>
<div id="addDlg" class="dialog"><div class="box"><h3>Aggiungi articolo</h3><div class="formrow"><label>Articolo</label><input id="newName" placeholder="Nome articolo"></div><div class="formrow"><label>Giacenza iniziale</label><input id="newOpening" type="number" min="0" step="1" value="0"></div><div class="right"><button onclick="closeDlg('addDlg')">Annulla</button> <button class="primary" onclick="addArticle()">Aggiungi</button></div></div></div>
<div id="removeDlg" class="dialog"><div class="box"><h3>Rimuovi articolo</h3><p>L'articolo verrà rimosso dal mese selezionato e dai mesi successivi. I mesi precedenti resteranno invariati.</p><div class="formrow"><label>Articolo</label><select id="removeName" style="flex:1;padding:8px"></select></div><div class="right"><button onclick="closeDlg('removeDlg')">Annulla</button> <button class="danger" onclick="removeArticle()">Rimuovi</button></div></div></div>
<div id="archivesDlg" class="dialog"><div class="box"><h3>Storico mensile</h3><div id="archivesBody"></div><div class="right"><button onclick="closeDlg('archivesDlg')">Chiudi</button></div></div></div>
<script>
let state=null;
const fmt=n=>{const x=Math.round((Number(n)||0)*100)/100;return Number.isInteger(x)?String(x):x.toFixed(2)};
const esc=s=>String(s).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
function toast(msg){const e=document.getElementById('toast');e.textContent=msg;e.style.display='block';clearTimeout(window.__tt);window.__tt=setTimeout(()=>e.style.display='none',2200)}
async function api(url,opt={}){const r=await fetch(url,{headers:{'Content-Type':'application/json'},...opt});let j={};try{j=await r.json()}catch{};if(!r.ok)throw Object.assign(new Error(j.error||'Errore'),{status:r.status});return j}
async function reloadState(){state=await api('/api/state');render()}
function totals(item){const u=Object.values(item.unloads||{}).reduce((a,b)=>a+(Number(b)||0),0);const g=(Number(item.opening)||0)+(Number(item.load)||0);return {g,u,c:g-u}}
function labelDate(iso){const [y,m,d]=iso.split('-');return d+'/'+m+'/'+y}
function monthLabel(k){const [y,m]=k.split('-');return new Date(+y,+m-1,1).toLocaleDateString('it-IT',{month:'long',year:'numeric'}).toUpperCase()}
function render(){
  const mp=document.getElementById('monthPicker');mp.value=state.currentMonth;mp.min=state.minMonth;
  document.getElementById('saveHint').textContent='File dati: '+state.dataFile;
  const m=state.month, dates=state.dates;
  const end=new Date(Number(state.currentMonth.slice(0,4)),Number(state.currentMonth.slice(5,7)),0);const endLabel=String(end.getDate()).padStart(2,'0')+'/'+String(end.getMonth()+1).padStart(2,'0')+'/'+end.getFullYear();
  let h='<thead><tr><th class="opening">GIACENZA</th><th class="article">ARTICOLO</th>';
  for(const d of dates)h+='<th class="day">'+labelDate(d)+'</th>';
  h+='<th class="total">SCARICO</th><th class="load">CARICO</th><th class="closing">MAGAZZINO AL '+endLabel+'</th></tr></thead><tbody>';
  const colTot=Object.fromEntries(dates.map(d=>[d,0]));let sumG=0,sumU=0,sumL=0,sumC=0;
  for(const it of m.items){const t=totals(it);sumG+=t.g;sumU+=t.u;sumL+=Number(it.load)||0;sumC+=t.c;
    h+='<tr><td class="opening"><input type="number" min="0" step="1" value="'+fmt(t.g)+'" data-name="'+esc(it.name)+'" data-field="giacenza" title="Giacenza = base mese + carico"></td><td class="article">'+esc(it.name)+'</td>';
    for(const d of dates){const v=Number((it.unloads||{})[d])||0;colTot[d]+=v;h+='<td class="day"><input type="number" min="0" step="1" value="'+(v?fmt(v):'')+'" data-name="'+esc(it.name)+'" data-field="unload" data-date="'+d+'"></td>'}
    h+='<td class="total">'+fmt(t.u)+'</td><td class="load"><input type="number" min="0" step="1" value="'+((Number(it.load)||0)?fmt(it.load):'')+'" data-name="'+esc(it.name)+'" data-field="load"></td><td class="closing"><b>'+fmt(t.c)+'</b></td></tr>';
  }
  h+='</tbody><tfoot><tr><td>TOTALI</td><td></td>'+dates.map(d=>'<td>'+fmt(colTot[d])+'</td>').join('')+'<td>'+fmt(sumU)+'</td><td>'+fmt(sumL)+'</td><td>'+fmt(sumC)+'</td></tr></tfoot>';
  document.getElementById('grid').innerHTML=h;
  document.querySelectorAll('#grid input').forEach(i=>i.addEventListener('change',onCell));
  document.getElementById('summary').innerHTML='<div class="kpi"><span>Giacenza disponibile</span><b>'+fmt(sumG)+'</b></div><div class="kpi"><span>Totale scaricato</span><b>'+fmt(sumU)+'</b></div><div class="kpi"><span>Totale caricato</span><b>'+fmt(sumL)+'</b></div><div class="kpi"><span>Magazzino finale</span><b>'+fmt(sumC)+'</b></div>';
  const b=document.getElementById('archiveBadge'); if(m.archiveDirty){b.textContent='Archiviato, poi modificato';b.className='badge warn'}else if(m.archivedAt){b.textContent='Archiviato '+m.archivedAt;b.className='badge ok'}else{b.textContent='Mese non archiviato';b.className='badge'}
}
async function onCell(e){const el=e.target,name=el.dataset.name,field=el.dataset.field,date=el.dataset.date||'',v=Math.max(0,Number(el.value)||0);let sendField=field,sendValue=v;
 if(field==='giacenza'){const it=state.month.items.find(x=>x.name===name);sendField='opening';sendValue=v-(Number(it.load)||0);if(sendValue<0){toast('La giacenza non può essere inferiore al carico del mese');reloadState();return}}
 try{await api('/api/update',{method:'POST',body:JSON.stringify({month:state.currentMonth,name,field:sendField,date,value:sendValue})});await reloadState()}catch(err){toast(err.message);await reloadState()}}
document.getElementById('monthPicker').addEventListener('change',async e=>{const old=state.currentMonth;try{state=await api('/api/switch',{method:'POST',body:JSON.stringify({month:e.target.value})});render();toast('Mese '+monthLabel(state.currentMonth)+' caricato')}catch(err){toast(err.message);e.target.value=old}})
function closeDlg(id){document.getElementById(id).classList.remove('open')}
function openAdd(){document.getElementById('newName').value='';document.getElementById('newOpening').value='0';document.getElementById('addDlg').classList.add('open');document.getElementById('newName').focus()}
async function addArticle(){try{await api('/api/add',{method:'POST',body:JSON.stringify({month:state.currentMonth,name:document.getElementById('newName').value,opening:Number(document.getElementById('newOpening').value)||0})});closeDlg('addDlg');await reloadState();toast('Articolo aggiunto')}catch(err){toast(err.message)}}
function openRemove(){const s=document.getElementById('removeName');s.innerHTML=state.month.items.map(i=>'<option>'+esc(i.name)+'</option>').join('');document.getElementById('removeDlg').classList.add('open')}
async function removeArticle(){const name=document.getElementById('removeName').value;if(!confirm('Rimuovere '+name+' dal mese selezionato e dai mesi successivi?'))return;try{await api('/api/remove',{method:'POST',body:JSON.stringify({month:state.currentMonth,name})});closeDlg('removeDlg');await reloadState();toast('Articolo rimosso')}catch(err){toast(err.message)}}
async function archiveMonth(overwrite){try{await api('/api/archive',{method:'POST',body:JSON.stringify({month:state.currentMonth,overwrite})});await reloadState();toast('Mese archiviato')}catch(err){if(err.status===409&&confirm(err.message))return archiveMonth(true);toast(err.message)}}
async function saveNow(){try{const j=await api('/api/save',{method:'POST',body:'{}'});toast('Dati salvati in '+j.file)}catch(err){toast(err.message)}}
function backup(){location.href='/api/backup';toast('Backup esportato')}
async function showArchives(){try{const a=await api('/api/archives');let h='';if(!a.length)h='<p>Nessun mese archiviato.</p>';for(const ar of a){let u=0,l=0,c=0;for(const r of ar.rows){u+=r.unload;l+=r.load;c+=r.closing}h+='<details class="archive-card"><summary>'+monthLabel(ar.month)+' — '+ar.archivedAt+' — finale '+fmt(c)+'</summary><table><thead><tr><th>Articolo</th><th>Giacenza iniziale</th><th>Carico</th><th>Scarico</th><th>Finale</th></tr></thead><tbody>'+ar.rows.map(r=>'<tr><td class="article">'+esc(r.name)+'</td><td>'+fmt(r.opening)+'</td><td>'+fmt(r.load)+'</td><td>'+fmt(r.unload)+'</td><td>'+fmt(r.closing)+'</td></tr>').join('')+'</tbody></table></details>'}document.getElementById('archivesBody').innerHTML=h;document.getElementById('archivesDlg').classList.add('open')}catch(err){toast(err.message)}}
async function quitApp(){if(!confirm('Chiudere Letizia? I dati sono già salvati.'))return;try{await api('/api/shutdown',{method:'POST',body:'{}'})}catch{};document.body.innerHTML='<div style="font-family:Segoe UI;padding:40px"><h2>Letizia è stata chiusa.</h2><p>Puoi chiudere questa scheda del browser.</p></div>'}
reloadState().catch(e=>toast(e.message));
</script></body></html>`
