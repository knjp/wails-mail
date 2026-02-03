import {useState, useEffect} from 'react';
// import {GetMessages} from "../wailsjs/go/main/App";
import {GetMessages, GetMessageBody} from "../wailsjs/go/main/App";
import './App.css';

//                        <div className="body">{selectedMsg.snippet}...</div>
//                        <div className="body" dangerouslySetInnerHTML={{ __html: fullBody }} />

function App() {
    const [messages, setMessages] = useState([]);
    const [selectedMsg, setSelectedMsg] = useState(null);
    const [fullBody, setFullBody] = useState("");

    const handleSelect = async (m) => {
        setSelectedMsg(m);
        setFullBody("読み込み中...");
        try {
            const body = await GetMessageBody(m.id);
            console.log("取得した本文:", body); // ブラウザのコンソールで確認
            setFullBody(body);
        } catch (err) {
            setFullBody("読み込みエラー: " + err);
        }
        //    const body = await GetMessageBody(m.id);
        // setFullBody(body);
    };

    useEffect(() => {
        GetMessages().then(setMessages);
    }, []);

    return (
        <div className="container">
            {/* 左ペイン：メール一覧 */}
            <div className="sidebar">
                {messages.map((m) => (
                    <div key={m.id} className="mail-item" onClick={() => handleSelect(m)}>
                        <div className="subject">{m.subject || "(件名なし)"}</div>
                        <div className="from">{m.from}</div>
                    </div>
                ))}
            </div>

            {/* 右ペイン：プレビュー */}
            {/* 右ペイン（メインコンテンツ）の修正案*/}
            <div className="main-content">
                {selectedMsg ? (
                    <div className="email-view">
                        {/* ヘッダーセクション */}
                        <div className="email-header">
                            <h1 className="email-subject">{selectedMsg.subject}</h1>
                            <div className="email-meta">
                                <span className="avatar">{selectedMsg.from[0].toUpperCase()}</span>
                                <div className="meta-info">
                                    <div className="email-from">{selectedMsg.from}</div>
                                    <div className="email-date">2024/XX/XX</div> {/* 本来はDateヘッダーから取得 */}
                                </div>
                            </div>
                        </div>
                        
                        {/* 本文セクション: iframeで隔離して表示 */}
                        <div className="email-body-container">
                            <iframe
                                title="email-content"
                                className="email-body-frame"
                                srcDoc={fullBody} // ここにHTMLを直接流し込む
                                sandbox="allow-popups" // セキュリティ確保
                            />
                        </div>
                    </div>
                ) : (
                    <div className="empty-state">
                        <p>表示するメールを選択してください</p>
                    </div>
                )}
            </div>
        </div>
    );
}

export default App;
