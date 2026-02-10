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
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
	_ "modernc.org/sqlite"

	"github.com/ollama/ollama/api"
)

type MessageSummary struct {
	ID         string `json:"id"`
	From       string `json:"from"`
	Subject    string `json:"subject"`
	Snippet    string `json:"snippet"`
	Importance string `json:"importance"`
	Date       string `json:"date"`
	Deadline   string `json:"deadline"`
}

type ChannelConfig struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

type Channel struct {
	Name string `json:"name"`
}

type App struct {
	ctx    context.Context
	srv    *gmail.Service
	db     *sql.DB
	store  *Store
	ollama *api.Client
}
type SearchResult struct {
	ID    string  `json:"id"`
	Score float32 `json:"score"`
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

	a.loadChannelsFromJson()

	// テーブル作成
	a.db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY, sender TEXT, subject TEXT, snippet TEXT, timestamp DATETIME,
		body TEXT,
		summary TEXT,
		is_read INTEGER DEFAULT 0,
		importance INTEGER DEFAULT 0,
		deadline DATETIME
	);`)
	a.db.Exec(`CREATE TABLE IF NOT EXISTS channels (id INTEGER PRIMARY KEY, name TEXT UNIQUE, sql_condition TEXT);`)

	// 差出人で検索・ソートするためのインデックス
	a.db.Exec("CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender);")

	// 日付（今日、今週など）で検索・ソートするためのインデックス
	a.db.Exec("CREATE INDEX IF NOT EXISTS idx_messages_timestamp ON messages(timestamp);")
	a.db.Exec("CREATE INDEX IF NOT EXISTS idx_messages_deadline ON messages(deadline);")

	fmt.Println("✅ インデックスの作成/確認が完了しました")

	s, err := NewStore(a.db)
	if err != nil {
		panic(err)
	}
	a.store = s

	ollama_client, _ := api.ClientFromEnvironment()
	a.ollama = ollama_client

	// Gmail API の初期化 (credentials.json と token.json がある前提)
	// a.srv = srv
	// --- ここから Gmail API の初期化を再開 ---
	b, err := os.ReadFile("conf/credentials.json")
	if err != nil {
		log.Printf("credentials.json 読み込み失敗: %v", err)
		return
	}

	config, err := google.ConfigFromJSON(b, gmail.GmailModifyScope)
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
	tokFile := "conf/token.json"
	f, err := os.Open(tokFile)
	if err != nil {
		// token.json がない場合、認証URLを生成して表示
		authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
		fmt.Println("\n--- 🔑 Google 認証が必要です ---")
		fmt.Println("以下のURLをブラウザで開き、表示されたコードをここに入力してください:")
		fmt.Printf("\n%v\n\n", authURL)

		var authCode string
		fmt.Print("認証コードを入力: ")
		if _, err := fmt.Scan(&authCode); err != nil {
			return nil, fmt.Errorf("コードの読み取りに失敗: %v", err)
		}

		tok, err := config.Exchange(context.TODO(), authCode)
		if err != nil {
			return nil, fmt.Errorf("トークン取得に失敗: %v", err)
		}

		// 新しい通行証（token.json）を保存
		saveToken(tokFile, tok)
		return config.Client(context.Background(), tok), nil
		//return nil, err
	}
	defer f.Close()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return config.Client(context.Background(), tok), err
}

// トークン保存用ヘルパー
func saveToken(path string, token *oauth2.Token) {
	fmt.Printf("トークンを保存中: %s\n", path)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("保存失敗: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
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

	query := fmt.Sprintf("SELECT id, sender, subject, snippet, importance, deadline, datetime(timestamp, '+9 hours') as jst_time FROM messages WHERE %s ORDER BY timestamp DESC", condition)
	// query := fmt.Sprintf("SELECT id, sender, subject, snippet, timestamp FROM messages WHERE %s ORDER BY timestamp DESC", condition)
	rows, err := a.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MessageSummary
	for rows.Next() {
		var m MessageSummary
		var ts string
		rows.Scan(&m.ID, &m.From, &m.Subject, &m.Snippet, &m.Importance, &m.Deadline, &ts)
		m.Date = ts
		results = append(results, m)
	}
	return results, nil
}

func (a *App) markAsRead(id string) error {
	if a.srv == nil {
		return nil
	}
	// ラベル変更リクエストの作成
	batch := &gmail.BatchModifyMessagesRequest{
		RemoveLabelIds: []string{"UNREAD"},
		Ids:            []string{id},
	}
	// Googleサーバーへ送信
	err := a.srv.Users.Messages.BatchModify("me", batch).Do()
	if err != nil {
		return err
	}

	_, err = a.db.Exec("UPDATE messages SET is_read = 1 WHERE id = ?", id)
	return err
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

	// gmail で既読に変更
	go func() {
		err := a.markAsRead(id)
		if err != nil {
			fmt.Printf("既読同期失敗: %v\n", err)
		}
	}()

	body := a.extractBody(msg.Payload)

	// 3. 次回のために SQLite に保存（キャッシュ）しておく
	go func() {
		_, err = a.db.Exec("UPDATE messages SET body = ? WHERE id = ?", body, id)
		if err != nil {
			fmt.Printf("キャッシュ保存エラー: %v\n", err)
		}
	}()

	go func(msgID string, text string) {
		// テキストが空でなければベクトル化して保存
		if text != "" {
			err := a.SyncEmailVector(msgID, text)
			if err != nil {
				fmt.Printf("AI学習失敗: %v\n", err)
			}
		}
	}(id, body)

	go func(msgID string, content string) {
		if content != "" {
			fmt.Printf("🤖 Ollama 要約開始: %s\n", msgID)
			_, err := a.SummarizeEmail(msgID) // 先ほど作成したキャッシュ機能付き関数
			if err != nil {
				fmt.Printf("Ollama 要約失敗: %v\n", err)
			} else {
				fmt.Printf("✅ Ollama 要約完了: %s\n", msgID)
				// 必要なら Wails のイベントで React に「できたよ！」と通知も可能
				// runtime.EventsEmit(a.ctx, "summary_ready", msgID)
			}
		}
	}(id, body)

	return body, nil
}

/*
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
*/

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

func (a *App) SyncHistoricalMessages(pageToken string) (string, error) {
	if a.srv == nil {
		return "", fmt.Errorf("SyncHistoricalMessage: API未初期化")
	}

	// 1. 最新500件を取得（pageTokenがあれば続きから）
	req := a.srv.Users.Messages.List("me").MaxResults(500)
	if pageToken != "" {
		req.PageToken(pageToken)
	}
	res, err := req.Do()
	if err != nil {
		return "", err
	}

	// 2. 500通をループして保存・更新
	for _, m := range res.Messages {
		// metadata形式で「ラベル情報」も含めて取得
		msg, err := a.srv.Users.Messages.Get("me", m.Id).Format("metadata").Do()
		if err != nil {
			continue
		}

		// 既読判定（UNREADラベルがあるか）
		isRead := 1
		for _, label := range msg.LabelIds {
			if label == "UNREAD" {
				isRead = 0
				break
			}
		}

		// ヘッダー解析（差出人・件名）
		var sender, subject string
		for _, h := range msg.Payload.Headers {
			if h.Name == "From" {
				sender = h.Value
			}
			if h.Name == "Subject" {
				subject = h.Value
			}
		}

		// 時間処理（JST変換）
		t := time.Unix(0, msg.InternalDate*int64(time.Millisecond)).In(time.FixedZone("Asia/Tokyo", 9*60*60))
		ts := t.Format("2006-01-02 15:04:05")

		// 【重要】INSERT OR REPLACE で、既読状態も最新に更新
		_, err = a.db.Exec(`
			INSERT OR REPLACE INTO messages (id, sender, subject, snippet, timestamp, is_read) 
			VALUES (?, ?, ?, ?, ?, ?)`,
			msg.Id, sender, subject, msg.Snippet, ts, isRead)
	}

	// 次のページの合言葉を返す
	return res.NextPageToken, nil
}

// AISearch は「あいまい検索」を実行して、スコアの高い順に ID を返します
func (a *App) AISearch(query string) ([]SearchResult, error) {
	// 1. 検索クエリをベクトル化
	req := &api.EmbeddingRequest{
		Model:  "nomic-embed-text",
		Prompt: query,
	}
	resp, err := a.ollama.Embeddings(context.Background(), req)
	if err != nil {
		return nil, err
	}
	queryVec := resp.Embedding

	// 2. DBから全データを取得（本来は専門のベクトルDBを使いますが、数千通ならこれで爆速です）
	rows, err := a.db.Query("SELECT id, vector FROM email_vectors")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var allResults []SearchResult
	for rows.Next() {
		var id string
		var vecBytes []byte
		rows.Scan(&id, &vecBytes)

		var dbVec []float32
		if err := json.Unmarshal(vecBytes, &dbVec); err != nil {
			continue
		}

		// 3. 類似度（ドット積）の計算
		var score float32
		for i := 0; i < len(queryVec); i++ {
			score += float32(queryVec[i]) * float32(dbVec[i])
		}
		allResults = append(allResults, SearchResult{ID: id, Score: score})
	}

	// 4. スコアが高い順（降順）にソート
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Score > allResults[j].Score
	})

	// 上位10件程度を返す（Wailsのフロントエンドへ）
	if len(allResults) > 10 {
		return allResults[:10], nil
	}
	return allResults, nil
}

// GetAISearchResults は AI 検索の結果を元に、メッセージ詳細のリストを返します
func (a *App) GetAISearchResults(query string) ([]MessageSummary, error) {
	// 1. まずは既存の AISearch ロジックで ID とスコアを取得
	// (先ほど作った AISearch 関数を流用するか、そのロジックをここに書く)
	searchResults, err := a.AISearch(query)
	if err != nil {
		return nil, err
	}

	// 2. ID だけの配列を作る
	var ids []string
	for _, res := range searchResults {
		ids = append(ids, res.ID)
	}

	// 3. DB から詳細情報を取得（a.store は db.go で作った Store）
	msgs, err := a.store.GetMessagesByIDs(ids)
	if err != nil {
		return nil, err
	}

	fmt.Printf("msgs: %s\n", msgs)
	return msgs, nil
}

func (a *App) SummarizeEmail(id string) (string, error) {
	// 1. キャッシュチェック
	var cached string

	a.db.QueryRow("SELECT summary FROM messages WHERE id = ?", id).Scan(&cached)
	if len(cached) > 0 {
		return cached, nil
	}

	// 2. 本文取得
	var body string
	a.db.QueryRow("SELECT body FROM messages WHERE id = ?", id).Scan(&body)
	if len(body) == 0 {
		return "本文がありません", nil
	}

	// 3. Ollama 呼び出し
	//ollamaModel1 := "llama3.1:8b-instruct-q4_K_M"
	//ollamaModel1 := "schroneko/gemma-2-2b-jpn-it" // または "llama3" など
	ollamaModel2 := "llama3.1:8b-instruct-q4_K_M"

	prompt1 := fmt.Sprintf(`
あなたは多忙なビジネスマン専用の要約エージェントです。
以下のルールを厳守し、メールを要約してください。

- 内容を【3行以内】の箇条書きに要約すること。
- 挨拶や「以下が要約です」という説明は一切不要。
- 本文をそのままコピーせず、要点のみを再構成すること。
- 日本語で出力すること。

メール内容: %s`, body)

	req := &api.GenerateRequest{
		Model: ollamaModel2,
		//Prompt: "以下のメールを3行で要約してください。要約のみを示してください、説明などはいりません。:\n\n" + body,
		Prompt: prompt1,
		Stream: new(bool), // false
	}

	var summary string
	err := a.ollama.Generate(a.ctx, req, func(resp api.GenerateResponse) error {
		summary = resp.Response
		return nil
	})
	if err != nil {
		return "", err
	}
	// --- 🔴 無粋なタグを掃除する 🔴 ---
	summary = strings.ReplaceAll(summary, "</start_of_turn>", "")
	summary = strings.ReplaceAll(summary, "</end_of_turn>", "")
	summary = strings.TrimSpace(summary) // 前後の余計な改行も消す
	// ------------------------------

	prompt2 := "次の内容を10文字程度で一言で表してください。\n\n" + summary
	shortSummary := &api.GenerateRequest{
		Model:  ollamaModel2,
		Prompt: prompt2,
		Stream: new(bool), // false
	}

	var summary2 string
	err = a.ollama.Generate(a.ctx, shortSummary, func(resp api.GenerateResponse) error {
		summary2 = resp.Response
		return nil
	})
	if err != nil {
		return "", err
	}

	prompt3 := "この要約を元に、重要度を1〜5の数字1文字だけで判定してください。1は広告、5は至急です。\n\n" + summary2
	importanceStr := &api.GenerateRequest{
		Model:  ollamaModel2,
		Prompt: prompt3,
		Stream: new(bool), // false
	}

	var importance string
	err = a.ollama.Generate(a.ctx, importanceStr, func(resp api.GenerateResponse) error {
		importance = resp.Response
		return nil
	})
	if err != nil {
		return "", err
	}
	re := regexp.MustCompile(`\d`)
	match := re.FindString(importance)
	finalVal := 0
	if match != "" {
		finalVal, _ = strconv.Atoi(match)
	}

	// 4. SQLite にキャッシュ
	a.db.Exec("UPDATE messages SET summary = ?, importance = ? WHERE id = ?", summary, finalVal, id)

	prompt4 := fmt.Sprintf(`
以下のメール本文から、返信期限、打合せ、イベント等の【最も重要な未来の日付】を1つだけ特定してください。
- 形式：YYYY-MM-DD (例: 2024-02-14)
- 今日は %s です。
- 「来週」「明日」などは今日を基準に計算してください。
- 日付が見当たらない場合は「なし」とだけ出力してください。
- 解説は一切不要です。

メール内容:
%s`, time.Now().Format("2006-01-02"), body)

	deadlineReq := &api.GenerateRequest{
		Model:  ollamaModel2,
		Prompt: prompt4,
		Stream: new(bool),
	}

	var deadlineStr string
	err = a.ollama.Generate(a.ctx, deadlineReq, func(resp api.GenerateResponse) error {
		deadlineStr = resp.Response
		return nil
	})
	// --- 正規表現で YYYY-MM-DD を抽出 ---
	reDate := regexp.MustCompile(`\d{4}-\d{2}-\d{2}`)
	finalDate := reDate.FindString(deadlineStr)

	if finalDate != "" {
		a.db.Exec("UPDATE messages SET deadline = ? WHERE id = ?", finalDate, id)
		fmt.Printf("📅 期限を検出: %s (ID: %s)\n", finalDate, id)
	}

	return summary, nil
}

func (a *App) TrashMessage(id string) error {
	if a.srv == nil {
		return fmt.Errorf("Gmail APIが初期化されていません")
	}

	// 1. Googleサーバー上のメールをゴミ箱(TRASH)へ移動
	// DeleteではなくTrashを使うのが「安全装置」としてのプロの選択
	_, err := a.srv.Users.Messages.Trash("me", id).Do()
	if err != nil {
		return fmt.Errorf("Gmailサーバーでのゴミ箱移動に失敗: %v", err)
	}

	// 2. サーバー側が成功した時のみ、ローカルの SQLite からも削除
	// これにより DB とサーバーの不整合を防ぐ (ストラ氏が喜ぶ整合性)
	_, err = a.db.Exec("DELETE FROM messages WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("ローカルDBの更新に失敗: %v", err)
	}

	fmt.Printf("🗑️ ゴミ箱へ移動完了: %s\n", id)
	return nil
}
