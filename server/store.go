package main

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

// Limits vale por ID de máquina. Zero = sem limite.
type Limits struct {
	MBps     float64 `json:"mbps"`     // banda máxima (entrada + saída) em MB/s
	Circuits int     `json:"circuits"` // sessões relay simultâneas
	DayMB    int64   `json:"dayMB"`    // franquia por dia
	MonthMB  int64   `json:"monthMB"`  // franquia por mês
}

const defaultID = "*"

type Entry struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // block | allow
	Note    string `json:"note"`
	Created int64  `json:"created"`
}

type Store struct{ db *sql.DB }

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS limits(id TEXT PRIMARY KEY, mbps REAL, circuits INT, day_mb INT, month_mb INT);
CREATE TABLE IF NOT EXISTS acl(id TEXT PRIMARY KEY, kind TEXT, note TEXT, created INT);
CREATE TABLE IF NOT EXISTS usage(id TEXT, day TEXT, bytes INT, PRIMARY KEY(id, day));
CREATE TABLE IF NOT EXISTS kv(k TEXT PRIMARY KEY, v TEXT);
INSERT OR IGNORE INTO limits VALUES('*', 5, 4, 2048, 20480);`)
	return &Store{db}, err
}

func (s *Store) Limit(id string) (Limits, error) {
	var l Limits
	err := s.db.QueryRow(`SELECT mbps,circuits,day_mb,month_mb FROM limits WHERE id=?`, id).Scan(&l.MBps, &l.Circuits, &l.DayMB, &l.MonthMB)
	return l, err
}

// EffectiveLimit usa o override do ID e, sem ele, o padrão global.
func (s *Store) EffectiveLimit(id string) Limits {
	if l, err := s.Limit(id); err == nil {
		return l
	}
	l, _ := s.Limit(defaultID)
	return l
}

func (s *Store) SetLimit(id string, l Limits) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO limits VALUES(?,?,?,?,?)`, id, l.MBps, l.Circuits, l.DayMB, l.MonthMB)
	return err
}

func (s *Store) DelLimit(id string) error {
	if id == defaultID {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM limits WHERE id=?`, id)
	return err
}

func (s *Store) Overrides() (map[string]Limits, error) {
	rows, err := s.db.Query(`SELECT id,mbps,circuits,day_mb,month_mb FROM limits`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]Limits{}
	for rows.Next() {
		var id string
		var l Limits
		if err := rows.Scan(&id, &l.MBps, &l.Circuits, &l.DayMB, &l.MonthMB); err != nil {
			return nil, err
		}
		m[id] = l
	}
	return m, rows.Err()
}

func (s *Store) AddEntry(e Entry) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO acl VALUES(?,?,?,?)`, e.ID, e.Kind, e.Note, time.Now().Unix())
	return err
}

func (s *Store) DelEntry(id string) error {
	_, err := s.db.Exec(`DELETE FROM acl WHERE id=?`, id)
	return err
}

func (s *Store) Entries() ([]Entry, error) {
	rows, err := s.db.Query(`SELECT id,kind,note,created FROM acl ORDER BY created DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Kind, &e.Note, &e.Created); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) EntryKind(id string) string {
	var k string
	s.db.QueryRow(`SELECT kind FROM acl WHERE id=?`, id).Scan(&k)
	return k
}

func (s *Store) AddUsage(id, day string, n int64) error {
	_, err := s.db.Exec(`INSERT INTO usage VALUES(?,?,?) ON CONFLICT(id,day) DO UPDATE SET bytes=bytes+excluded.bytes`, id, day, n)
	return err
}

// UsageSince soma os bytes do ID desde o dia (formato 2006-01-02) em diante.
func (s *Store) UsageSince(id, day string) int64 {
	var n sql.NullInt64
	s.db.QueryRow(`SELECT SUM(bytes) FROM usage WHERE id=? AND day>=?`, id, day).Scan(&n)
	return n.Int64
}

type Top struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
}

func (s *Store) TopSince(day string, n int) ([]Top, error) {
	rows, err := s.db.Query(`SELECT id,SUM(bytes) b FROM usage WHERE day>=? GROUP BY id ORDER BY b DESC LIMIT ?`, day, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Top{}
	for rows.Next() {
		var t Top
		if err := rows.Scan(&t.ID, &t.Bytes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Get(k string) string {
	var v string
	s.db.QueryRow(`SELECT v FROM kv WHERE k=?`, k).Scan(&v)
	return v
}

func (s *Store) Set(k, v string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO kv VALUES(?,?)`, k, v)
	return err
}
