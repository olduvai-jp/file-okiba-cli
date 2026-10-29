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

## ビルド・自動検査

Goの必要バージョンは`go.mod`を参照してください。

```sh
go build -trimpath -o bin/file-okiba ./cmd/file-okiba
go test -race ./...
go vet ./...
```

テストは一時ディレクトリとHTTPテストサーバーを使います。実サービス・別リポジトリ・接続済み端末・認証情報の準備は不要です。

転送のテストは、中断再開・作成応答消失・送信中の入力変更・取得先の衝突・ETagと途中データの整合性を対象にしています。認証情報を付けたリダイレクトは禁止し、通常操作と管理操作の認証を分離します。

OSごとのファイル権限・配置処理は、プラットフォーム別のGoファイルで実装します。新しいOSやファイルシステムに対応する際は、秘密ファイルのアクセス制御と、既存ファイルを上書きしない配置処理を確認してください。

## 配布

```sh
go run ./scripts/release -version vX.Y.Z
```

対応OS・CPUはREADMEと`scripts/release`で管理します。`dist/`の各アーカイブには実行ファイル・README・MITライセンスを含め、`SHA256SUMS`を併せて生成します。

GitHub Actionsは標準GitHub-hosted runnerを使用し、push/PRでLinux・macOS・Windowsの検査を実行します。`v*`タグでは検査成功後に全対象をビルドし、GitHub Releasesへ公開します。
