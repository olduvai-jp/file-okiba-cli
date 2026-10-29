# 開発ガイド

## 構成

| パス | 責務 |
|---|---|
| `cmd/file-okiba` | 実行エントリーポイント、シグナル・端末状態の処理 |
| `internal/api` | HTTP通信、URL・応答検証、再試行 |
| `internal/config` | プロファイル、秘密設定の保存と権限 |
| `internal/transfer` | multipart送信、永続再開状態、取得・衝突処理 |
| `internal/cli` | コマンド、表示、対話ウィザード |
| `scripts/release` | 配布対象のクロスビルド・アーカイブ・チェックサム |
| `scripts/integration` | ローカルサービスとの結合試験 |

## ビルド・自動検査

Goの必要バージョンは`go.mod`を参照してください。

```sh
go build -trimpath -o bin/file-okiba ./cmd/file-okiba
go test -race ./...
go vet ./...
```

転送のテストは、中断再開・作成応答消失・送信中の入力変更・取得先の衝突・ETagと途中データの整合性を対象にしています。認証情報を付けたリダイレクトは禁止し、通常操作と管理操作の認証を分離します。

## ローカル結合試験

隣の`../file-okiba`にサービスのソースとインストール済みnpm依存関係が必要です。テスト用アダプターで実際のWorkerと全D1マイグレーションをMiniflareに読み込み、隔離したD1/R2で実行します。親サービスのソース・本番データ・設定は変更しません。

S3のinit/completeと署名先へのPUT/GETはローカルR2へ接続します。実Cloudflare R2の署名受理は、この試験の検証範囲に含みません。テスト用接続先はlocalhostに限定し、TLS検証は維持します。

### サービス起動

Node.js、OpenSSL、親サービスの開発依存関係を使います。

```sh
mkdir -p .local/test/tls
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout .local/test/tls/key.pem \
  -out .local/test/tls/cert.pem -days 1 \
  -subj '/CN=*.r2.cloudflarestorage.com' \
  -addext 'subjectAltName=DNS:*.r2.cloudflarestorage.com'
node scripts/integration/server.mjs ../file-okiba .local/test/tls
```

サービスは`127.0.0.1:8788`、ストレージ用テストプロキシは`127.0.0.1:8789`で待ち受けます。終了時はCtrl-Cで停止してください。テストデータは終了時に破棄されます。

### 転送・管理・プロセス中断

別端末で実行します。

```sh
go test -c -o .local/test/integration-cli ./cmd/file-okiba
node scripts/integration/check.mjs .local/test/integration-cli
node scripts/integration/interrupt.mjs .local/test/integration-cli
```

macOSは通常のGoバイナリで`SSL_CERT_FILE`を参照しないため、テスト用エントリーポイントだけでCAをロードします。この処理は配布バイナリには入りません。

`check.mjs`はプロファイル、2-part転送、内容一致、重複防止、衝突処理、管理操作を検証します。`interrupt.mjs`は最初のpart送信後にSIGINTで中止し、別プロセスで再開して内容と重複の有無を検証します。

### ARM Linux実機

SSH/SCPで接続でき、`dd`・`sha256sum`・`/usr/bin/time`があるテスト端末を指定してください。テスト用一時ファイルとlocalhostへのreverse forwardingを使用し、終了時に削除・解除します。

```sh
go run ./scripts/release -version dev
mkdir -p .local/test/arm-release
tar -xzf dist/file-okiba_dev_linux_arm64.tar.gz \
  -C .local/test/arm-release file-okiba
node scripts/integration/arm-check.mjs SSH_HOST .local/test/arm-release/file-okiba
```

Linuxの`SSL_CERT_FILE`でテストCAを指定するため、こちらは配布用の単体バイナリを使用できます。送信・取得のSHA-256一致、完了済み送信の再実行、管理削除と最大RSSを確認します。

## 配布

```sh
go run ./scripts/release -version vX.Y.Z
```

対応OS・CPUはREADMEと`scripts/release`で管理します。`dist/`の各アーカイブには実行ファイル・README・MITライセンスを含め、`SHA256SUMS`を併せて生成します。

GitHub Actionsは標準GitHub-hosted runnerを使用し、push/PRでLinux・macOS・Windowsの検査を実行します。`v*`タグでは検査成功後に全対象をビルドし、GitHub Releasesへ公開します。
