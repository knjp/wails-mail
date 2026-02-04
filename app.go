package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
	_ "modernc.org/sqlite"
)

type MessageSummary struct {
	ID      string `json:"id"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Snippet string `json:"snippet"`
	Date    string `json:"date"`
}

type ChannelConfig struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

type Channel struct {
	Name string `json:"name"`
}

type App struct {
	ctx context.Context
	srv *gmail.Service
	db  *sql.DB
}

func NewApp() *App {
	return &App{}
}

func (a *App) loadChannelsFromJson() {
	data, err := os.ReadFile("conf/channels.json")
	if err != nil {
		return
	} // ファイルがなければスキップ

	var configs []ChannelConfig
	json.Unmarshal(data, &configs)

	// DBのチャンネル情報を一旦クリアして入れ直す（または差分更新）
	a.db.Exec("DELETE FROM channels")
	for _, c := range configs {
		a.db.Exec("INSERT INTO channels (name, sql_condition) VALUES (?, ?)", c.Name, c.Query)
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	os.MkdirAll("db", 0755)

	db, err := sql.Open("sqlite", "db/mail_cache.db")
	if err != nil {
		log.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("PRAGMA busy_timeout=5000")

	a.db = db

	// テーブル作成
	a.db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY, sender TEXT, subject TEXT, snippet TEXT, timestamp DATETIME, body TEXT
	);`)
	a.db.Exec(`CREATE TABLE IF NOT EXISTS channels (id INTEGER PRIMARY KEY, name TEXT UNIQUE, sql_condition TEXT);`)

	a.loadChannelsFromJson()

	// Gmail API の初期化 (credentials.json と token.json がある前提)
	// a.srv = srv
	// --- ここから Gmail API の初期化を再開 ---
	b, err := os.ReadFile("conf/credentials.json")
	if err != nil {
		log.Printf("credentials.json 読み込み失敗: %v", err)
		return
	}

	config, err := google.ConfigFromJSON(b, gmail.GmailReadonlyScope)
	if err != nil {
		log.Printf("OAuth config 作成失敗: %v", err)
		return
	}

	// getClient 関数を使って http.Client を取得
	client, err := a.getClient(config)
	if err != nil {
		log.Printf("Client 取得失敗 (token.json を確認してください): %v", err)
		return
	}

	// サービスを構造体のフィールドに代入（これで「API未初期化」が消えます）
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Printf("Gmail サービス作成失敗: %v", err)
		return
	}
	a.srv = srv
}

// getClient は token.json を読み込んで http.Client を返します
func (a *App) getClient(config *oauth2.Config) (*http.Client, error) {
	f, err := os.Open("conf/token.json")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return config.Client(context.Background(), tok), err
}

func (a *App) SyncMessages() error {
	if a.srv == nil {
		return fmt.Errorf("API未初期化")
	}
	res, err := a.srv.Users.Messages.List("me").MaxResults(20).Do()
	if err != nil {
		return err
	}

	for _, m := range res.Messages {
		msg, err := a.srv.Users.Messages.Get("me", m.Id).Format("metadata").Do()
		if err != nil {
			continue
		}

		// 時間処理
		// msInt, _ := strconv.ParseInt(msg.InternalDate, 10, 64)
		t := time.Unix(0, msg.InternalDate*int64(time.Millisecond))
		timestampStr := t.Format("2006-01-02 15:04:05")

		var sender, subject string
		for _, h := range msg.Payload.Headers {
			if h.Name == "From" {
				sender = h.Value
			}
			if h.Name == "Subject" {
				subject = h.Value
			}
		}

		a.db.Exec(`INSERT OR IGNORE INTO messages (id, sender, subject, snippet, timestamp) VALUES (?, ?, ?, ?, ?)`,
			msg.Id, sender, subject, msg.Snippet, timestampStr)
	}
	return nil
}

func (a *App) GetChannels() ([]Channel, error) {
	rows, err := a.db.Query("SELECT name FROM channels")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []Channel
	for rows.Next() {
		var c Channel
		rows.Scan(&c.Name)
		res = append(res, c)
	}
	return res, nil
}

func (a *App) GetMessagesByChannel(channelName string) ([]MessageSummary, error) {
	var condition string
	err := a.db.QueryRow("SELECT sql_condition FROM channels WHERE name = ?", channelName).Scan(&condition)
	if err != nil {
		condition = "1=1"
	}

	query := fmt.Sprintf("SELECT id, sender, subject, snippet, timestamp FROM messages WHERE %s ORDER BY timestamp DESC", condition)
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MessageSummary
	for rows.Next() {
		var m MessageSummary
		var ts string
		rows.Scan(&m.ID, &m.From, &m.Subject, &m.Snippet, &ts)
		m.Date = ts
		results = append(results, m)
	}
	return results, nil
}

func (a *App) GetMessageBody(id string) (string, error) {
	// 1. まずは SQLite に本文が保存されていないか確認
	var cachedBody string
	err := a.db.QueryRow("SELECT body FROM messages WHERE id = ?", id).Scan(&cachedBody)

	// DBに本文（長さ1以上）があれば、それを即座に返す
	if err == nil && len(cachedBody) > 0 {
		fmt.Printf("Cache Hit! ID: %s (SQLiteから取得)\n", id)
		return cachedBody, nil
	}

	// 2. なければ Gmail API から取得
	fmt.Printf("Cache Miss! ID: %s (APIから取得中...)\n", id)
	msg, err := a.srv.Users.Messages.Get("me", id).Format("full").Do()
	if err != nil {
		return "", err
	}

	body := a.extractBody(msg.Payload)

	// 3. 次回のために SQLite に保存（キャッシュ）しておく
	go func() {
		_, err = a.db.Exec("UPDATE messages SET body = ? WHERE id = ?", body, id)
		if err != nil {
			fmt.Printf("キャッシュ保存エラー: %v\n", err)
		}
	}()

	return body, nil
}

func (a *App) GetMessageBody_simple(id string) (string, error) {
	msg, err := a.srv.Users.Messages.Get("me", id).Format("full").Do()
	if err != nil {
		return "", err
	}

	// 簡易的な本文抽出（HTML優先）
	body := a.extractBody(msg.Payload)
	fmt.Printf("取得したメール(ID: %s) の本文サイズ: %d 文字\n", id, len(body))

	return body, nil
}

func (a *App) extractBody(part *gmail.MessagePart) string {
	// プレーンテキストの場合 (text/plain)
	if part.MimeType == "text/plain" && part.Body.Data != "" {
		data, _ := base64.URLEncoding.DecodeString(part.Body.Data)
		// テキストの改行を HTML の改行に変換し、URLをリンク化する等の処理
		// 手っ取り早くは <pre> タグで囲むのが確実です
		return "<pre style='white-space: pre-wrap; font-family: sans-serif;'>" + string(data) + "</pre>"
	}

	// HTMLの場合 (text/html)
	if part.MimeType == "text/html" && part.Body.Data != "" {
		data, _ := base64.URLEncoding.DecodeString(part.Body.Data)
		return string(data)
	}

	// マルチパート（再帰的に探す）
	for _, subPart := range part.Parts {
		if body := a.extractBody(subPart); body != "" {
			return body
		}
	}
	return ""
}
