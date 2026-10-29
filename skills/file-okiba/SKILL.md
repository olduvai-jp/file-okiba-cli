---
name: file-okiba
description: Use when the user requests file-okiba uploads, downloads, shared URLs, CLI profile setup, or group administration, or provides a file-okiba group or batch URL.
---

# file-okiba CLI

PATH上の `file-okiba` を使う。Go製の単体CLIで、Node.js・npm・サービスのソースは不要。まず `command -v file-okiba` と `file-okiba --version` を確認し、必要なサブコマンドの `--help` で導入済み版のオプションを確認する。未導入なら[配布ページ](https://github.com/olduvai-jp/file-okiba-cli/releases)から対象OS/CPUのアーカイブとSHA256SUMSを取得し、照合して導入する。

人間が操作する場合は `file-okiba` または `file-okiba wizard`。エージェント・スクリプトは通常コマンドに **`--json`** を付ける。成功JSONはstdout、進捗とエラーJSONはstderr。通常表示もJSON風だが、機械処理では常に `--json` を明示する。

## 対象と権限

- グループURL `/g/GROUP_TOKEN` は一覧・追加が可能な権限URL。既存プロファイルや `FILE_OKIBA_GROUP_URL` があれば利用する。
- バッチURL `/g/shared/b/BATCH_TOKEN` は受け取り用。共有したい相手へ渡すのはアップロード結果の **`batchUrl`**。グループURLを受取人へ渡さない。
- `FILE_OKIBA_DOWNLOAD_PASSWORD` は送信時のパスワード設定／取得時の認証に使う。管理は別の `FILE_OKIBA_ADMIN_TOKEN`（全グループ共通権限）。秘密値を引数へ直書きしない。
- グループURL・バッチURL・セッショントークン・署名URL・パスワード・管理トークンを公開ログやGitへ載せない。応答は必要な件数・ファイル名・保存先だけ抽出して確認し、バッチURLは共有を依頼された範囲で渡す。
- 接続先はプロファイルまたは環境変数で確認する。`--base-url` は管理用で、転送先は指定したURLのオリジン。省略時の管理接続先は本番。

## 操作の選択

| 目的 | コマンド |
|---|---|
| グループ／バッチ詳細 | `file-okiba inspect [URL] --json` |
| グループのバッチ一覧／バッチのファイル一覧 | `file-okiba list [URL] --json` |
| 通常ファイルを1バッチに送る | `file-okiba upload report.pdf photo.png --json` |
| 保持期間を指定 | `file-okiba upload report.pdf --retention-days 14 --json` |
| バッチ全件を取得 | `file-okiba download BATCH_URL --output ./received --json` |
| 一部だけ取得 | `file-okiba download BATCH_URL --file FILE_TOKEN --output ./received --json` |
| 期限付き署名URLが必要 | `file-okiba links BATCH_URL --json` |

`upload` は設定済みグループを使える。別のグループを明示する場合は `upload GROUP_URL FILE...`。`list` のファイルトークンは `--file` で使う共有用識別子で、管理APIの固定IDとは異なる。

ディレクトリはzip/tar化して通常ファイルとして送る。stdin、自動圧縮・展開、0バイトのアップロードには非対応。保持日数は既定7日（プロファイルで変更可）、選択肢は1・3・5・7・14・30・60・100。並列数はメモリ優先の既定1で、必要時に `--concurrency N` を指定する。

### 出力の扱い

URLを含む応答は非公開の一時ディレクトリへ捕捉する。例は設定済みグループへのアップロード:

```sh
umask 077
result_dir=$(mktemp -d)
trap 'rm -rf "$result_dir"' EXIT HUP INT TERM
if file-okiba upload ./report.pdf --json >"$result_dir/result.json" 2>"$result_dir/error.log"; then
  cli_status=0
else
  cli_status=$?
fi
# JSONを解析して完了ファイル・件数を確認する。生の応答はログへ出さない。
```

送信は `completed` の件数・ファイル名と `batchUrl` の有無、取得は `downloaded`・`skippedExisting` と実際の `path` を確認する。元ハッシュがある場合は取得物と照合する。サイズ一致だけで内容一致とは判断しない。APIには内容チェックサムがない。

## 再開・衝突・失敗

- 通信断・プロセス終了後は**同じグループURL、ファイル順、保持日数で同じuploadコマンドを再実行**する。サーバーの期限内で記録済みpartから再開する。完了済みなら同じバッチの結果を返し、新規送信しない。
- 同じファイルを意図して別バッチで再送する依頼なら `file-okiba upload report.pdf --restart --yes --json`。期限切れ・`source_changed` は自動で再送せず、明示的に新規送信する場合だけ `--restart` を使う。
- `outcome_unknown` はバッチ／セッション作成や変更の応答消失。勝手に新規作成を繰り返さず、一覧・詳細で成否を確認する。部分成功はエラーの `details` にバッチ結果・完了分が含まれる。
- DLも同じコマンドで再開する。途中状態は出力先の `.file-okiba/`、upload状態は設定ファイル隣の `state/`（`FILE_OKIBA_STATE_DIR` で変更可能）。状態を消すと再開情報を失う。
- DLの同名ファイルは既定で **rename**（例 `report.1.pdf`）。`--existing=rename|skip|overwrite|error` で指定。skipは内容一致の保証ではなく、overwriteは置換を依頼された場合に使う。実保存先は結果の `path`。
- 終了コード: 0成功、2入力／形式不正、3認証、4通信、5成否不明、6部分成功、130中止、1その他。終了コードだけで全ファイル成功と判断しない。

## プロファイル

`profile list --json` は秘密を伏せて表示する。既存設定を使い、接続先・秘密情報を推測して登録しない。

```sh
file-okiba profile create new --from existing --group-url GROUP_URL --json
file-okiba profile use new --json
file-okiba profile update new --admin-token-env FILE_OKIBA_ADMIN_TOKEN --json
```

`--from` は作成時コピーで、後からの継承ではない。実行時の優先順位は引数 → 環境変数 → `--profile NAME`／既定プロファイル。設定ファイルは `--config PATH` または `FILE_OKIBA_CONFIG` で変更できる。

## グループ管理

管理を依頼されたときだけ [管理操作](references/admin.md) を読む。転送だけなら管理トークンは不要。
