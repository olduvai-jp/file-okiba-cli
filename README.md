# file-okiba CLI

[file-okiba](https://github.com/olduvai-jp/file-okiba)のファイル転送・管理を行うGo製CLIです。Node.js不要の単体実行ファイルとして動作します。

## 導入

GitHub ReleasesからOS・CPUに合うアーカイブと`SHA256SUMS`を取得し、チェックサムを確認して展開してください。実行ファイル名は`file-okiba`（Windowsは`file-okiba.exe`）です。

| OS | CPU |
|---|---|
| Linux | amd64 / arm64 / armv7 |
| macOS | arm64（Apple Silicon） |
| Windows | amd64 / arm64 |

Linux版はCGOなし、armv7版はGOARM=7でビルドします。ARMv5・MIPS・OpenWrt全機種への対応は保証しません。macOS版は未署名のため、ダウンロード時のOSの実行許可が必要になる場合があります。

ソースからビルドする場合はGo 1.26以上を使います。

```sh
go build -trimpath -o bin/file-okiba ./cmd/file-okiba
./bin/file-okiba --help
```

## 対話ウィザード

```sh
file-okiba
# または
file-okiba wizard
```

端末で引数なし実行すると、設定登録・切り替え、アップロード、ファイル選択ダウンロード、詳細・一覧・リンク、管理操作を選べます。管理トークンとダウンロードパスワードの入力は画面に表示しません。パイプなど非対話環境の引数なし実行はヘルプを表示し、入力待ちしません。

## プロファイル

```sh
file-okiba profile create work-a --group-url 'https://file-okiba.oldv.org/g/GROUP_TOKEN'
file-okiba profile create work-b --from work-a --group-url 'https://file-okiba.oldv.org/g/OTHER_TOKEN'
file-okiba profile use work-b
file-okiba profile list
file-okiba profile update work-b --retention-days 14
file-okiba profile delete work-a --yes
```

`--from`は作成時コピーです。コピー元を変更しても、作成済みの設定には影響しません。

管理トークンはウィザードで入力するか、既存の環境変数から登録できます。トークン値を引数へ直接書く必要はありません。

```sh
# FILE_OKIBA_ADMIN_TOKENは秘密管理ツールなどで事前に設定
file-okiba profile update work-b --admin-token-env FILE_OKIBA_ADMIN_TOKEN
```

設定はmacOSでは`~/Library/Application Support/file-okiba/config.json`、Linuxでは`$XDG_CONFIG_HOME/file-okiba/config.json`（未設定なら`~/.config/...`）、Windowsではユーザーの設定ディレクトリに保存します。`--config PATH`または`FILE_OKIBA_CONFIG`で変更できます。Unixでは0600、Windowsでは実行ユーザーのみの保護DACLで秘密ファイルを保存します。プロファイル一覧はグループURLと管理トークンを伏せます。暗号化保存ではありません。

優先順位は**コマンド引数 → 環境変数 → 選択プロファイル → 既定値**です。`--profile NAME`で実行時だけ切り替えられます。

| 環境変数 | 用途 |
|---|---|
| `FILE_OKIBA_BASE_URL` | 管理API接続先。既定`https://file-okiba.oldv.org` |
| `FILE_OKIBA_GROUP_URL` | アップロード・参照先のグループURL |
| `FILE_OKIBA_ADMIN_TOKEN` | 全グループ共通の管理トークン |
| `FILE_OKIBA_DOWNLOAD_PASSWORD` | アップロード時の保護／取得時のパスワード |
| `FILE_OKIBA_CONFIG` | 設定ファイル |
| `FILE_OKIBA_STATE_DIR` | アップロードの再開状態保存先 |

`--base-url`は管理API用、転送は渡したURLの接続先を使います。HTTPS必須、localhost・127.0.0.1・::1のローカル開発だけHTTPを許可します。

## 転送

```sh
file-okiba inspect                         # 設定済みグループの詳細
file-okiba list                            # 設定済みグループのバッチ一覧
file-okiba upload report.pdf photo.png     # 1バッチにまとめる
file-okiba upload GROUP_URL report.pdf --retention-days 14
file-okiba inspect BATCH_URL
file-okiba list BATCH_URL
file-okiba links BATCH_URL --json
file-okiba download BATCH_URL --output ./received
file-okiba download BATCH_URL --file FILE_TOKEN --output ./received
```

通常ファイルのみ対応します。ディレクトリ構造が必要なら、利用者がzip/tar化して送ってください。標準入力・自動圧縮・自動展開には対応しません。現APIが受け付けない0バイトファイルは送信前に拒否します。

保持期間は既定7日、選択肢は`1,3,5,7,14,30,60,100`日です。期限は送信完了ではなく、サーバーでセッションを作った時点から進みます。

ファイル全体や100 MiBのpartをメモリに展開せず、範囲ストリームで送ります。並列数は既定1、`--concurrency N`（1〜32、実際はサーバー上限以内）で変更可能です。開始時と再開時にSHA-256をストリーム計算し、ローカルファイルの変更を確認します。

### 再開と部分成功

アップロードは同じURL・ファイル順・保持日数のコマンドを再実行すると、保存したセッションとサーバーに記録済みのpartを照合して再開します。状態は設定ファイルの隣の`state/`に保存します。完了済みの同じコマンドは同じ結果を返します。意図して再送する場合は`--restart`を使います。

```sh
file-okiba upload report.pdf
# 通信断・Ctrl-C後、同じコマンドで再開
file-okiba upload report.pdf
# 意図して新しいバッチを作る場合
file-okiba upload report.pdf --restart --yes
```

再開はサーバー期限内のみです（現設定では最終活動から24時間、かつファイル保持期限内）。期限切れ時は勝手に新規作成しません。バッチ／セッション作成の応答を失った場合も`outcome_unknown`として停止し、自動で重複作成しません。管理一覧で成否を確認してから明示的に`--restart`してください。転送失敗時には完了分・共有URL・状態パスをエラーの`details`に含めます。

DLの途中状態は保存先の`.file-okiba/`に保存します。同じコマンドで再開し、強いETagとRange応答が確認できる場合だけ追記します。サーバーがRangeを受け付けない場合は最初から取得し直します。状態ファイルを失うと再開できません。

APIは内容チェックサムを提供しないため、DLの検証はTLS・ファイルトークン・ETagによる同一オブジェクトの確認とサイズ照合までです。元ファイルとの厳密な一致は別途SHA-256等で確認してください。

### 同名ファイル

`--existing=rename|skip|overwrite|error`で選択できます。

| 値 | 挙動 |
|---|---|
| `rename`（既定） | `report.1.pdf`等に変更して保存 |
| `skip` | 存在する通常ファイルをスキップ。内容一致の判定はしない |
| `overwrite` | 取得完了後に既存の通常ファイルを置き換える |
| `error` | 衝突時に停止 |

通常ファイル以外への上書き、危険なファイル名は拒否します。JSONの`path`は実際の保存先です。

## 管理

管理トークンは利用者ごとの権限ではなく、全グループに操作権限を持ちます。対応する管理APIがサーバー側で有効になっている必要があります。

```sh
file-okiba group list --limit 50 --json
file-okiba group list --cursor NEXT_CURSOR --json
file-okiba group create --name '受け渡し'
file-okiba group inspect GROUP_ID
file-okiba group update GROUP_ID --name '新しい名前'
file-okiba group disable GROUP_ID --yes
file-okiba group enable GROUP_ID
file-okiba group rotate-url GROUP_ID --yes
file-okiba group files list GROUP_ID
file-okiba group files inspect GROUP_ID FILE_ID
file-okiba group files delete GROUP_ID FILE_ID --yes
file-okiba group batches list GROUP_ID
file-okiba group batches inspect GROUP_ID BATCH_ID
file-okiba group batches delete GROUP_ID BATCH_ID --yes
file-okiba group delete GROUP_ID --force --yes
```

管理操作は共有トークンではなく固定IDを使います。一覧はページ単位で、`nextCursor`を次の`--cursor`に渡します（既定50・最大200）。グループURLは作成・再発行時のみ取得できます。

削除・無効化・URL再発行は対話確認し、非対話では`--yes`必須です。中身のあるグループ削除には`--force`も必要です。削除APIの202応答は同じDELETEを続け、完了を確認してから成功を返します。途中で失敗したら同じコマンドで再開できます。

## 自動化の出力契約

全コマンドで`--json`を使えます。成功結果はstdoutのJSON、進捗とエラーはstderrです。通常表示は読みやすく整形したJSONです。共有URL・署名URLを返すコマンドの結果には権限を含むため、公開ログへ貼らないでください。

| 終了コード | 意味 |
|---|---|
| 0 | 成功 |
| 1 | その他の失敗 |
| 2 | 引数・入力・応答形式不正 |
| 3 | 認証・権限エラー |
| 4 | 通信・転送失敗 |
| 5 | 変更操作の成否不明 |
| 6 | 一部ファイル成功後の失敗 |
| 130 | 中止 |

エラー形式は`{"error":{"code":"...","message":"...","status":400,"details":{...}}}`。`status`と`details`は省略されることがあります。再開に必要な権限値はローカル状態に保存します。

## エージェント用スキル

使い方・再開・秘密情報の扱いをまとめた[スキル](skills/file-okiba/SKILL.md)を同梱しています。CLIをPATHへ導入したうえで、スキルのフォルダーをコピーしてください。サービスのソースやNode.jsは不要です。

```sh
mkdir -p "$HOME/.agents/skills"
cp -R skills/file-okiba "$HOME/.agents/skills/"
```

同名のスキルがある場合は、そのフォルダーをスキル検索先の外へ退避してからコピーします。導入後は新しいエージェントセッションで `$file-okiba` を指定するか、file-okibaでの転送・管理を依頼してください。

## 開発・リリース

```sh
go test -race ./...
go vet ./...
go run ./scripts/release -version v0.1.0
```

配布物は`dist/`に6構成のアーカイブと`SHA256SUMS`を生成します。公開リポジトリの標準GitHub-hosted runnerでLinux/macOS/Windowsを検証し、`v*`タグpush時にGitHub Releasesへ公開します。self-hosted runnerは使いません。

構成・テスト手順は[開発ガイド](docs/development.md)を参照してください。
