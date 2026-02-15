import {useState, useEffect} from 'react';
import './App.css';
import {SyncMessages, GetMessagesByChannel, GetMessageBody, GetChannels, SyncHistoricalMessages, GetAISearchResults, SummarizeEmail, TrashMessage} from "../wailsjs/go/main/App";

function App() {
    const [messages, setMessages] = useState([]);
    const [tabs, setTabs] = useState([]);
    const [activeTab, setActiveTab] = useState("All");
    const [selectedMsg, setSelectedMsg] = useState(null);
    const [fullBody, setFullBody] = useState("");
    const [loadingBody, setLoadingBody] = useState(false);
    const [loading, setLoading] = useState(false);
    const [nextPageToken, setNextPageToken] = useState("");
    const [query, setQuery] = useState("");
    const [summary, setSummary] = useState("")
    //const [results, setResults] = useState([]);
    const [relatedMsgs, setRelatedMsgs] = useState([])
    const [isSummarizing, setIsSummarizing] = useState(false);

    const handleManualSummarize = async () => {
        setIsSummarizing(true);
        const sum = await SummarizeEmail(selectedMsg.id);
        setSummary(sum);
        setIsSummarizing(false);
    };

    const handleLoadMore = async () => {
        setLoading(true);
        // Goを呼び出して、次のトークンを受け取る
        const token = await SyncHistoricalMessages(nextPageToken);
        setNextPageToken(token);

        // 表示を更新
        const data = await GetMessagesByChannel(activeTab);
        setMessages(data);
        setLoading(false);
    };

    const handleAISearch = async () => {
        console.log("AI Searching!! for:", query)
        try {
            const results = await GetAISearchResults(query);
            console.log("Search Results:", results); // ここで中身を確認！

            if(results && results.length > 0){
                setMessages(results);
                setActiveTab("🔍 検索結果");
            } else {
                alert("該当するメールが見つかりませんでした。");
            }
        } catch (err) {
            console.error("検索失敗:", err);
        }
    };

    const handleDelete = async (msg) => {
        // ストラ氏も安心の確認ダイアログ
        if (!window.confirm(`「${msg.subject}」をゴミ箱に移動しますか？`)) return;
    
        try {
            await TrashMessage(msg.id);
            // 成功したら、現在のリストからそのメールを消す（再読み込み不要の爆速UI）
            setMessages(prev => prev.filter(m => m.id !== msg.id));
            setSelectedMsg(null);
        } catch (err) {
            alert("削除に失敗しました: " + err);
        }
    };

    const getDaysLeft = (deadline) => {
        if (!deadline || deadline === "なし") return null;
        const today = new Date();
        const target = new Date(deadline);
        const diffTime = target - today;
        const diffDays = Math.ceil(diffTime / (1000 * 60 * 60 * 24));
        return diffDays;
    };

    const loadChannels = async (retryCount = 0) => {
        try {
            const res = await GetChannels();
            if((!res || res.length === 0) && retryCount < 20){
                console.log("Channels are not ready! Retry ...");
                setTimeout(() => loadChannels(retryCount + 1), 5000);
                return;
            }
            if (res) setTabs(res.map(c => c.name));
        } catch(err) {
            console.error("Read Error:", err);
        }
    };

    // 1. 初期起動時にチャンネル一覧を取得
    useEffect(() => {
       loadChannels();
    }, []);

    // 2. タブ切り替え時にデータを取得
    useEffect(() => {
        const loadData = async () => {
            const data = await GetMessagesByChannel(activeTab);
            setMessages(data || []);
            // バックグラウンドで同期
            SyncMessages().then(async () => {
                const freshData = await GetMessagesByChannel(activeTab);
                setMessages(freshData || []);
            });
        };
        loadData();
    }, [activeTab]);

    const handleSelect = async (msg) => {
    if (loadingBody) return;

    setSelectedMsg(msg);
    setFullBody("読み込み中...");
    setRelatedMsgs([]);
    setSummary("");
    setLoadingBody(true);

    // --- 1. 【爆速】手元のスニペットで関連検索を即座に開始 ---
    // 要約を待たないので、クリックした瞬間に右ペインが埋まり始めます
    GetAISearchResults(msg.snippet).then(related => {
        if (related) {
            setRelatedMsgs(related.filter(r => r.id !== msg.id));
        }
    }).catch(err => console.error("関連検索エラー:", err));

    try {
        // --- 2. 本文取得 ---
        const body = await GetMessageBody(msg.id);
        setFullBody(body);

        // --- 3. 本文が取れたら要約を開始 ---
        // これも非同期で行い、でき次第表示する
        //SummarizeEmail(msg.id).then(sum => {
        //    setSummary(sum);
        // });

    } catch (err) {
        console.error("本文取得エラー:", err);
        setFullBody("エラーが発生しました。");
    } finally {
        setLoadingBody(false);
    }

    // 既読反映などのためのリスト更新
    setTimeout(async () => {
        const data = await GetMessagesByChannel(activeTab);
        setMessages(data || []);
    }, 500);
};

    const handleSelect2 = async (msg) => {
        if (loadingBody) return; // すでに読み込み中なら無視
    
        setSelectedMsg(msg);
        setFullBody("読み込み中...");
        setRelatedMsgs([])

        setSummary("");
        setLoadingBody(true); // ロック開始
    
        try {
            const body = await GetMessageBody(msg.id);
            setFullBody(body);
        } catch (err) {
            console.error("本文取得エラー:", err);
            setFullBody("エラーが発生しました。");
        } finally {
            setLoadingBody(false); // ロック解除
        }

        if (msg.Snippet) {
            const related = await GetAISearchResults(msg.Snippet);
            setRelatedMsgs(related.filter(r => r.id !== msg.id));
        }

        setTimeout(async () => {
            const data = await GetMessagesByChannel(activeTab);
            setMessages(data || []);
        }, 500);
    };

    //
    // メッセージリストを日付順に整理
    //
    const renderMessageList = () => {
        let lastGroup = ""; // 直前のグループを記憶

        const myAddress = "kiyoshi@tmu.ac.jp";
        const now = new Date();
        const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();

        return messages.map((m) => {
            const msgDate = new Date(m.timestamp);
            const msgTime = msgDate.getTime();

            let currentGroup = "";
            if (msgTime >= todayStart) {
                currentGroup = "今日";
            } else if (msgTime >= todayStart - (7 * 24 * 60 * 60 * 1000)) {
                currentGroup = "1週間以内";
            } else if (msgTime >= todayStart - (30 * 24 * 60 * 60 * 1000)) {
                currentGroup = "1ヶ月以内";
            } else {
                currentGroup = "それ以前";
            }
    
            const displayDate = msgDate.toLocaleString('ja-JP');
            // --- グループが変わった時だけセパレーターを出す ---
            const showSeparator = currentGroup !== lastGroup;
            lastGroup = currentGroup;

            const isDirect = m.recipient && m.recipient.includes(myAddress);
            const isML = m.recipient && !isDirect; // 自分宛でなければML（またはCC）とみなす

            return (
                <div key={m.id}>
                    {showSeparator && (
                        <div className="list-separator">{currentGroup}</div>
                    )}
                    <div
                        className={`mail-item ${selectedMsg?.id === m.id ? 'selected' : ''} importance-${m.importance}`}
                        onClick={() => handleSelect(m)}
                    >
                        <div className="subject">
                            {/* 🌟 宛先バッジを追加 🌟 */}
                            {isDirect ? (
                                <span className="recipient-badge direct">TO ME</span>
                            ) : isML ? (
                                <span className="recipient-badge ml">ML</span>
                            ) : null}

                            {m.subject}
                            {m.importance >= 4 && (
                                <span className={`importance-badge level-${m.importance}`}>
                                    {m.importance === 5 ? "🔥 CRITICAL" : "⚡ IMPORTANT"}
                                </span>
                            )}
                        </div>
                        <div className='list-snippet'> {m.snippet} </div>
                        <div className="from">{m.from}</div>
                        <div className="mail-date">{displayDate}</div>
                    </div>
                </div>
            );
        });
    };

    const daysLeft = selectedMsg ? getDaysLeft(selectedMsg.deadline) : null;


    return (
        <div className="container">
            <div className="main-layout">

                {/* 左端：チャンネルリスト（旧タブバー） */}
                <div className="channel-sidebar">

                    {/* 検索エリア */}
                    <div className="search-bar">
                        <input 
                            type="text" 
                            placeholder="AIであいまい検索..." 
                            value={query}
                            onChange={(e) => setQuery(e.target.value)}
                            onKeyDown={(e) => e.key === 'Enter' && handleAISearch(e.target.value)}
                        />
                        <button onClick={handleAISearch}>検索</button>
                    </div>

                    <div className="sidebar-header">CHANNELS</div>
                    {tabs.map(name => (
                        <div 
                            key={name} 
                            className={`channel-item ${activeTab === name ? 'active' : ''}`}
                            onClick={() => setActiveTab(name)}
                        >
                            # {name}
                        </div>
                    ))}
                </div>

                {/* 中央：メールリスト */}
                <div className="mail-list-pane">
                    <div className="pane-header">{activeTab}</div>
                    <div className="list-container">
                        {messages.length === 0 && <div className="info">メールがありません</div>}

                        { renderMessageList() }

                        {messages.length>0 && (
                            <button onClick={handleLoadMore} disabled={loading} className="load-more">
                                {loading ? "読み込み中・・・" : "さらに500件読み込む"}
                            </button>
                        )}
                    </div>
                </div>

                <div className="main-content">
                    {selectedMsg ? (
                        <div className="email-view">
                            {/* 1. ヘッダー：件名と基本情報 */}
                            <div className="email-header-top">
                                <div className="header-main">
                                    <h2 className="detail-subject">{selectedMsg.subject}</h2>
                                    <div className="detail-meta">
                                        <div className="meta-row-meta">
                                            <span className="meta-label">From:</span>
                                            <span className="detail-from">{selectedMsg.from}</span>
                                        </div>
                                        <div className="meta-row">
                                            <span className="meta-label">To:</span>
                                            <span className="detail-to">{selectedMsg.recipient || "（宛先なし）"}</span>
                                        </div>
                                        <span className="detail-date">
                                            📅 {new Date(selectedMsg.timestamp).toLocaleString('ja-JP')}
                                        </span>
                                    </div>
                                </div>
                                
                                {/* 2. 右上のアクションボタン群 */}
                                <div className="header-actions">
                                    <button onClick={handleManualSummarize} disabled={isSummarizing} className="summary-btn">
                                        {isSummarizing ? "⌛..." : "✨ 要約"}
                                    </button>
                                    <button onClick={() => handleDelete(selectedMsg)} className="delete-btn">
                                        🗑️
                                    </button>
                                </div>
                            </div>

                            {/* 3. AI インフォメーション（期限と要約） */}
                            {(daysLeft !== null || summary) && (
                                <div className="ai-info-section">
                                    {daysLeft !== null && (
                                        <div className={`deadline-banner ${daysLeft < 0 ? 'overdue' : daysLeft <= 3 ? 'urgent' : ''}`}>
                                            <span className="icon">📅</span>
                                            <span className="text">
                                                {daysLeft < 0 ? `期限切れ (${Math.abs(daysLeft)}日経過)` : 
                                                 daysLeft === 0 ? "本日締切！" : 
                                                 `${selectedMsg.deadline} まであと ${daysLeft} 日`}
                                            </span>
                                        </div>
                                    )}
                                    {summary && <div className="ai-summary-content">{summary}</div>}
                                </div>
                            )}
                
                            {/* 4. 本文 */}
                            <div className="email-body-container">
                                <iframe
                                    key={selectedMsg.id}
                                    title="body"
                                    className="email-body-frame"
                                    srcDoc={fullBody} 
                                />
                            </div>
                        </div>
                    ) : <div className="empty-state">メールを選択してください</div>}
                </div>

                {/* 🌟 4つ目のペイン：関連コンテキスト 🌟 */}
                <div className="related-pane">
                    <div className="pane-header">🔗 関連・過去の経緯</div>
                    <div className="related-list-container">
                        {relatedMsgs.length === 0 && <div className="info">関連なし</div>}
                        {relatedMsgs.map(rm => (
                            <div key={rm.id} className="mail-item related-item" onClick={() => handleSelect(rm)}>
                                <div className="subject-small">{rm.subject}</div>
                                <div className="date-small">{new Date(rm.timestamp).toLocaleDateString()}</div>
                            </div>
                        ))}
                    </div>
                </div>

            </div>
        </div>
    );
}

export default App;
