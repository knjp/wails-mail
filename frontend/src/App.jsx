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
            if((!res || res.length === 0) && retryCount < 5){
                console.log("Channels are not ready! Retry ...");
                setTimeout(() => loadChannels(retryCount + 1), 500);
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
        if (loadingBody) return; // すでに読み込み中なら無視
    
        setSelectedMsg(msg);
        setFullBody("読み込み中...");
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

        SummarizeEmail(msg.id).then(res =>{
            setSummary(res);
        });

        setTimeout(async () => {
            const data = await GetMessagesByChannel(activeTab);
            setMessages(data || []);
        }, 500);
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
                        {messages.map(m => (
                            <div
                                key={m.id} 
                                className={`mail-item ${selectedMsg?.id === m.id ? 'selected' : ''}`} 
                                onClick={() => handleSelect(m)}
                            >
                                <div className="subject">{m.subject}
                                    {m.importance >= 4 && (
                                    <span className={`importance-badge level-${m.importance}`}>
                                        {m.importance === 5 ? "🔥 CRITICAL" : "⚡ IMPORTANT"}
                                    </span>
                                    )}
                                </div>

                                <div className="from">{m.from}</div>
                            </div>
                        ))}
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
                            <div className="email-header">
                                <h3>{selectedMsg.subject}</h3><h3>{selectedMsg.from}<br></br>{selectedMsg.date}</h3>
                                    {daysLeft !== null && (
                                        <div className={`deadline-banner ${daysLeft < 0 ? 'overdue' : daysLeft <= 3 ? 'urgent' : ''}`}>
                                            <span className="icon">📅</span>
                                            <span className="text">
                                            {daysLeft < 0 ? `期限切れ (${Math.abs(daysLeft)}日経過)` : 
                                             daysLeft === 0 ? "本日締切！" : 
                                            `期限まで あと ${daysLeft} 日 (${selectedMsg.deadline})`}
                                            </span>
                                        </div>
                                    )}

                                {summary && (
                                    <div className="ai-summary-card">
                                        <span className="ai-badge">AI SUMMARY</span>
                                        <p>{summary}</p>
                                    </div>
                                )}
                                <button onClick={() => handleDelete(selectedMsg)} className="delete-btn">
                                    🗑️ ゴミ箱へ
                                </button>
                            </div>
                            <div className="email-body-container">
                                <iframe
                                    key={selectedMsg.id}
                                    title="body"
                                    className="email-body-frame"
                                    srcDoc={fullBody} 
                                />
                            </div>
                        </div>
                    ) : <div className="empty-state">選択してください</div>}
                </div>
            </div>
        </div>
    );
}

export default App;
