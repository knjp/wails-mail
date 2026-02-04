import {useState, useEffect} from 'react';
import './App.css';
import {SyncMessages, GetMessagesByChannel, GetMessageBody, GetChannels} from "../wailsjs/go/main/App";

function App() {
    const [messages, setMessages] = useState([]);
    const [tabs, setTabs] = useState([]);
    const [activeTab, setActiveTab] = useState("All");
    const [selectedMsg, setSelectedMsg] = useState(null);
    const [fullBody, setFullBody] = useState("");
    const [loadingBody, setLoadingBody] = useState(false);


    // 1. 初期起動時にチャンネル一覧を取得
    useEffect(() => {
        GetChannels().then(res => {
            if (res) setTabs(res.map(c => c.name));
        });
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
    };

    return (
        <div className="container">
            <div className="tab-bar">
                {tabs.map(name => (
                    <button key={name} className={activeTab === name ? "active" : ""} onClick={() => setActiveTab(name)}>
                        {name}
                    </button>
                ))}
            </div>
            <div className="main-layout">
                <div className="sidebar">
                    {messages.length === 0 && <div className="info">メールがありません</div>}
                    {messages.map(m => (
                        <div key={m.id} className={`mail-item ${selectedMsg?.id === m.id ? 'selected' : ''}`} onClick={() => handleSelect(m)}>
                            <div className="subject">{m.subject}</div>
                            <div className="from">{m.from}</div>
                        </div>
                    ))}
                </div>
                <div className="main-content">
                    {selectedMsg ? (
                        <div className="email-view">
                            <div className="email-header"><h3>{selectedMsg.subject}</h3></div>
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
