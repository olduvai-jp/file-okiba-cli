# 管理操作

`file-okiba group --help` で導入版を確認する。認証は `FILE_OKIBA_ADMIN_TOKEN`、接続先は `FILE_OKIBA_BASE_URL` またはプロファイル。トークンは全グループを操作できるので、依頼された対象・操作だけ実行する。

一覧・詳細から接続先と固定IDを確認する。グループ名や共有URLのトークンから管理IDを推測しない。

```sh
file-okiba group list --limit 50 --json
file-okiba group list --cursor NEXT_CURSOR --json
file-okiba group inspect GROUP_ID --json
file-okiba group create --name '受け渡し' --json
file-okiba group update GROUP_ID --name '新しい名前' --json
file-okiba group files list GROUP_ID --json
file-okiba group files inspect GROUP_ID FILE_ID --json
file-okiba group batches list GROUP_ID --json
file-okiba group batches inspect GROUP_ID BATCH_ID --json
```

管理一覧は1ページずつ返る。`nextCursor` があれば次の `--cursor` へ渡す。既定50・最大200件。ファイル／バッチの管理IDは共有用トークンとは別物。

```sh
file-okiba group disable GROUP_ID --yes --json
file-okiba group enable GROUP_ID --json
file-okiba group rotate-url GROUP_ID --yes --json
file-okiba group files delete GROUP_ID FILE_ID --yes --json
file-okiba group batches delete GROUP_ID BATCH_ID --yes --json
file-okiba group delete GROUP_ID --force --yes --json
```

`--yes` は非対話の確認承認。削除・無効化・URL再発行が依頼の範囲内であることを確かめて使う。中身のあるグループ削除だけは `--force` も必要。削除は復元不可で、バッチ削除は中身も削除する。

削除はCLIが202応答の間同じDELETEを継続し、完了後に成功を返す。途中失敗時は成否を確認して同じ削除コマンドで再開できる。作成・URL再発行などの応答が不明な場合に、無条件で同じ変更を繰り返さない。

グループURLは作成・再発行時だけ取得できる。再発行しても既存のバッチ共有URLは維持される。無効化・再発行だけでは発行済みR2署名URLを直ちに取り消せない。

管理応答にも秘密URLが含まれる。SKILL.mdの出力捕捉方法を使い、結果を確認してから必要な内容だけ報告する。
