package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"

	"github.com/ollama/ollama/api"
	_ "modernc.org/sqlite"
)

type EmailRecord struct {
	ID      string
	Content string
	Vector  []float32
}

type Store struct {
	conn *sql.DB
}

// データベースの初期化
func NewStore(globalDB *sql.DB) (*Store, error) {
	db := globalDB

	// テーブル作成（ID, 本文, ベクトルを保存）
	sqlStmt := `CREATE TABLE IF NOT EXISTS email_vectors (
		id TEXT PRIMARY KEY, 
		content TEXT, 
		vector BLOB
	);`
	if _, err := db.Exec(sqlStmt); err != nil {
		return nil, err
	}
	return &Store{conn: db}, nil
}

// ベクトル化されたメールを保存
func (s *Store) SaveEmail(id, content string, vector []float32) error {
	vecJSON, _ := json.Marshal(vector)
	_, err := s.conn.Exec(
		"INSERT OR REPLACE INTO email_vectors (id, content, vector) VALUES (?, ?, ?)",
		id, content, vecJSON,
	)
	return err
}

// 全件取得（検索時に使用）
func (s *Store) GetAll() ([]EmailRecord, error) {
	rows, err := s.conn.Query("SELECT id, content, vector FROM email_vectors")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []EmailRecord
	for rows.Next() {
		var r EmailRecord
		var vecJSON []byte
		if err := rows.Scan(&r.ID, &r.Content, &vecJSON); err != nil {
			continue
		}
		json.Unmarshal(vecJSON, &r.Vector)
		records = append(records, r)
	}
	return records, nil
}

// SyncEmailVector は、特定のメールをベクトル化して保存します
func (a *App) SyncEmailVector(gmailID string, bodyText string) error {

	cleanText := cleanHTML(bodyText)

	// 1. Ollama に文章を送って 768 次元の数値（Embedding）をもらう
	req := &api.EmbeddingRequest{
		Model:  "nomic-embed-text",
		Prompt: cleanText,
	}

	// a.ollama は startup で初期化した *api.Client
	resp, err := a.ollama.Embeddings(context.Background(), req)
	if err != nil {
		return err
	}

	// 2. float32 の配列を DB に保存するために JSON (byte配列) に変換
	vectorBytes, err := json.Marshal(resp.Embedding)
	if err != nil {
		return err
	}

	// 3. SQLite に保存（gmail_id を主キーとして上書き保存）
	_, err = a.db.Exec(
		"INSERT OR REPLACE INTO email_vectors (id, content, vector) VALUES (?, ?, ?)",
		gmailID,
		cleanText,
		vectorBytes,
	)

	return err
}

func cleanHTML(html string) string {
	// タグ（<...>）をすべて空文字に置換
	re := regexp.MustCompile("<[^>]*>")
	return re.ReplaceAllString(html, "")
}

// GetMessagesByIDs は指定された複数のIDに合致するメッセージ情報を返します
func (s *Store) GetMessagesByIDs(ids []string) ([]MessageSummary, error) {
	if len(ids) == 0 {
		return []MessageSummary{}, nil
	}

	// SQLの "IN (?, ?, ?)" の部分を生成
	// IDの数だけ ? を並べる
	query := "SELECT id, subject, sender, timestamp FROM messages WHERE id IN ("
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		query += "?"
		args[i] = id
		if i < len(ids)-1 {
			query += ","
		}
	}
	query += ")"

	// IDの順番が検索スコア順なので、その順番を維持したい場合は
	// ここで並び替えの処理を足すこともできますが、まずは単純に取得します
	rows, err := s.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []MessageSummary
	for rows.Next() {
		var m MessageSummary
		if err := rows.Scan(&m.ID, &m.Subject, &m.From, &m.Date); err != nil {
			continue
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}
