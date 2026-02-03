package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type App struct {
	ctx context.Context
	srv *gmail.Service
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// 1. credentials.json を読み込んで config を作成
	b, err := os.ReadFile("conf/credentials.json")
	if err != nil {
		log.Fatalf("credentials.json を読み込めません: %v", err)
	}
	config, err := google.ConfigFromJSON(b, gmail.GmailReadonlyScope)
	if err != nil {
		log.Fatalf("OAuth config を作成できません: %v", err)
	}

	// 2. token.json からクライアントを作成
	client, err := a.getClient(config)
	if err != nil {
		log.Printf("認証済みクライアントの取得に失敗: %v", err)
		return
	}

	// 3. Gmail サービスを初期化
	srv, err := gmail.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Printf("Gmailサービス作成失敗: %v", err)
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

// フロントエンドから呼ばれる関数
func (a *App) GetLabels() ([]string, error) {
	if a.srv == nil {
		return nil, fmt.Errorf("Gmail API が準備できていません。token.json を確認してください")
	}

	res, err := a.srv.Users.Labels.List("me").Do()
	if err != nil {
		return nil, fmt.Errorf("API 実行エラー: %v", err)
	}

	var labels []string
	for _, l := range res.Labels {
		labels = append(labels, l.Name)
	}
	return labels, nil
}

type MessageSummary struct {
	ID      string `json:"id"`
	Snippet string `json:"snippet"`
	Subject string `json:"subject"`
	From    string `json:"from"`
}

func (a *App) GetMessages() ([]MessageSummary, error) {
	if a.srv == nil {
		return nil, fmt.Errorf("API未初期化")
	}

	res, err := a.srv.Users.Messages.List("me").MaxResults(15).Do()
	if err != nil {
		return nil, err
	}

	var summaries []MessageSummary
	for _, m := range res.Messages {
		// metadataを指定して件名と差出人だけ効率的に取得
		msg, err := a.srv.Users.Messages.Get("me", m.Id).Format("metadata").Do()
		if err != nil {
			continue
		}

		summary := MessageSummary{ID: msg.Id, Snippet: msg.Snippet}
		for _, h := range msg.Payload.Headers {
			if h.Name == "Subject" {
				summary.Subject = h.Value
			}
			if h.Name == "From" {
				summary.From = h.Value
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func (a *App) GetMessageBody(id string) (string, error) {
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

func (a *App) extractBody_old(part *gmail.MessagePart) string {
	if part.Body.Data != "" {
		data, _ := base64.URLEncoding.DecodeString(part.Body.Data)
		return string(data)
	}
	for _, subPart := range part.Parts {
		if body := a.extractBody(subPart); body != "" {
			return body
		}
	}
	return ""
}
