# traq-tui

`traq-tui` は、ターミナル作業の横で traQ を確認するための Linux-first な非公式 TUI クライアントです。公式クライアントの代替を目指すものではなく、OAuth で自分のユーザーとしてログインし、チャンネルの直近メッセージを読み、プレーンテキストを投稿する個人向け MVP です。

## 完成物の estimate

この MVP で得られるものは、次の機能を持つ単一の Go バイナリです。

- 約 30/70 の2ペイン UI（検索可能なチャンネル一覧とメッセージ一覧）
- 選択チャンネルの直近50件の読込と、既定5秒間隔のポーリング
- 複数行 composer からの投稿と、成功直後の再読込
- ユーザー OAuth Authorization Code Flow、loopback callback、token refresh
- token の原子的な保存（ディレクトリ `0700`、ファイル `0600`）
- 80×24を下限としたレスポンシブ表示、remote text の ANSI/control sequence 除去

規模は production Go 約2,100行、テスト約2,900行です。OAuth client が既に用意できていれば、初回セットアップと動作確認は通常5〜10分程度を想定しています。これは個人利用向け MVP の見積もりであり、WebSocket、未読管理、stamp、添付、DM などを備えた完全な traQ クライアントではありません。

## 必要環境

- Linux
- Go 1.26 以降
- traQ 上で作成した OAuth client
- `xdg-open`（任意。利用できなければ認可URLを端末へ表示します）
- 80×24以上の端末

OAuth client には `read` と `write` scope を許可し、loopback callback URL を登録してください。登録値と `TRAQ_REDIRECT_URL` は、host・port・path を含めて完全に一致させます。

## ビルド

```bash
go build -o traq-tui ./cmd/traq-tui
./traq-tui --help
```

## 設定

アプリケーションは `.env` を自動読込しません。以下の環境変数を shell から渡します。

| 変数 | 必須 | 内容 |
|---|---:|---|
| `TRAQ_API_BASE_URL` | yes | 対象 traQ の HTTPS API v3 URL。末尾は `/api/v3` |
| `TRAQ_CLIENT_ID` | yes | OAuth client ID |
| `TRAQ_CLIENT_SECRET` | yes | OAuth client secret |
| `TRAQ_REDIRECT_URL` | yes | 登録済みの HTTP loopback callback URL |
| `TRAQ_TUI_TOKEN_FILE` | no | token 保存先。既定は実行ディレクトリ基準の `.traq-tui/token.json` |
| `TRAQ_TUI_POLL_INTERVAL` | no | 正の Go duration。既定は `5s` |

[`.env.example`](.env.example) をローカルの `.env` にコピーして編集する場合は、秘密情報を commit せず、ファイルを `0600` にしてください。

```bash
cp .env.example .env
chmod 600 .env
# .env はローカルでのみ編集する
set -a
. ./.env
set +a
```

API URL は HTTPS かつ `/api/v3` で終わる必要があります。callback は `http://localhost:PORT/path`、`http://127.0.0.1:PORT/path`、または `http://[::1]:PORT/path` の形式に限られます。port は明示し、path は `/` 以外にしてください。

token 用ディレクトリが存在しない場合は `0700` で作成されます。既存ディレクトリを指定した場合、アプリはその権限を勝手に変更せず、`0700` でなければ安全のため失敗します。token path 内の symbolic link も拒否します。

## 起動と認証

```bash
# 保存済みtokenを使う。存在しなければ新規ログイン
./traq-tui

# 保存済みtokenを無視して認可をやり直す
./traq-tui --login

# 設定されたtokenファイルだけを削除する
./traq-tui --logout
```

初回ログインでは callback listener を起動してからブラウザを開きます。認可後の code を loopback callback で受け取り、token を保存します。callback はランダムな `state` を検証し、3分で timeout します。ブラウザを自動起動できない場合は、秘密パラメータを除去した手動認可URLが表示されます。

client secret、authorization code、access token、refresh token をチャット、issue、ログ、commit に貼らないでください。

## キー操作

| 場所 | キー | 動作 |
|---|---|---|
| Global | `ctrl+c` | 終了 |
| 通常時 | `q` | 終了（filter/composer 内では文字として入力） |
| Channel | `j` / `k`, `↓` / `↑` | カーソル移動 |
| Channel | `enter` | チャンネル選択、直近50件を取得 |
| Channel | `/` | channel path の絞り込み |
| Filter | `enter` | ハイライト中のチャンネルを選択 |
| Filter | `esc` | query を残して filter を閉じる |
| Pane | `tab` | channel/message pane を切り替える |
| Message | `j` / `k`, `↓` / `↑` | 1行スクロール |
| Message | `pgdown` / `pgup`, `ctrl+d` / `ctrl+u` | ページスクロール |
| Message | `g` / `G` | 最古 / 最新位置へ移動 |
| 通常時 | `r` | 選択チャンネルを即時再読込 |
| 通常時 | `i` | composer を開く |
| Composer | `enter` | 改行 |
| Composer | `ctrl+s` | 投稿 |
| Composer | `esc` | 下書きを残して閉じる |
| 通常時 | `?` | help の表示 / 非表示 |

空白だけの投稿と、送信中の重複 submit は拒否されます。投稿失敗時は下書きを維持し、成功時だけ消去して選択チャンネルを再読込します。

## 更新とエラー

ポーリング対象は選択中の1チャンネルだけです。各取得結果は channel ID と選択世代で検証されるため、チャンネル切替前の遅い応答が現在の画面を上書きしません。取得した50件の window を置換するため、同じメッセージを追加し続ける重複表示も行いません。

API エラーは既存メッセージを消さず status line に表示します。HTTP 401 の場合は次を実行して再認証してください。

```bash
./traq-tui --login
```

よくある問題:

- `listen for OAuth callback`: callback の port が使用中か、登録URLと設定が一致しているか確認する
- ブラウザが開かない: 端末に表示されたURLを同じ端末セッションのブラウザで開く
- callback timeout: 3分以内に認可を完了し、必要なら `--login` でやり直す
- token directory permission error: 保存先の専用ディレクトリを `chmod 700` にする
- token file permission error: token ファイルを `chmod 600` にする
- 画面が崩れる: 端末を80×24以上へ広げる
- network/rate-limit error: 現在の表示は保持されるため、待ってから `r` で再試行する

## セキュリティ

- OAuth scope は `read` と `write` のみ
- callback の `state` は暗号学的乱数で生成し、constant-time で比較
- token は一時ファイルへの書込・`fsync`・rename で原子的に更新
- token directory/file はそれぞれ `0700` / `0600`
- token path の通常ファイル性と symbolic link を検査
- API error は response body や bearer token を表示しない
- channel、user、message、remote error の ANSI CSI/OSC と制御文字を表示前に除去
- `xdg-open` は shell を介さず、URLを単一引数として起動

## テスト

自動テストは fake service と `httptest.Server` を使い、実 traQ や実ブラウザへ接続しません。

```bash
gofmt -l .
go test -race ./...
go vet ./...
go build -o /tmp/traq-tui ./cmd/traq-tui
```

## 実 traQ での最終確認

秘密情報はローカルで環境変数に設定し、次の順に確認します。

1. 投稿してよい test channel を決める。
2. `./traq-tui --login` を起動し、ブラウザで認可する。
3. test channel を選び、直近メッセージ・author・timestamp が読めることを確認する。
4. `i` で composer を開き、一意で無害な確認文を `ctrl+s` で投稿する。
5. TUI の再読込後に投稿が1回だけ表示されることを確認する。
6. 公式クライアントでも、認証した自分のユーザーによる同じ投稿を確認する。

自動テストが通っていても、この実環境確認が終わるまでは対象 traQ に対する受け入れ完了とはみなしません。

## MVP に含まれないもの

- WebSocket / リアルタイムイベント
- 未読・既読管理
- stamp
- 添付ファイル
- メッセージの編集・削除
- thread
- 全文検索
- direct message
- desktop notification
- traQ Markdown の完全な描画

traQ および関連名称は各権利者に帰属します。このプロジェクトは非公式です。
