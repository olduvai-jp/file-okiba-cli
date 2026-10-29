// Test the built binary against server.mjs; all data is local and disposable.
import {spawnSync} from 'node:child_process';
import {mkdtempSync,writeFileSync,readFileSync,mkdirSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {resolve,join} from 'node:path';
import assert from 'node:assert/strict';
const binary=resolve(process.argv[2]||'bin/file-okiba');
const work=mkdtempSync(join(tmpdir(),'file-okiba-cli-test-'));
const env={...process.env,FILE_OKIBA_INTEGRATION_CLI:'1',FILE_OKIBA_CONFIG:join(work,'config.json'),FILE_OKIBA_STATE_DIR:join(work,'state'),FILE_OKIBA_BASE_URL:'http://localhost:8788',FILE_OKIBA_ADMIN_TOKEN:'local-admin-token',HTTPS_PROXY:'http://localhost:8789',SSL_CERT_FILE:resolve('.local/test/tls/cert.pem')};
function command(args,expected=0){const r=spawnSync(binary,[...args,'--json'],{env,encoding:'utf8'});assert.equal(r.status,expected,`${args[0]} exit=${r.status} ${r.stderr}`);return JSON.parse(expected===0?r.stdout:r.stderr.trim().split('\n').at(-1));}
const g=command(['group','create','--name','Go integration']).group;
const payload=Buffer.alloc(6*1024*1024+17);for(let n=0;n<payload.length;n++)payload[n]=n%251;
const input=join(work,'payload.bin');writeFileSync(input,payload);
command(['profile','create','local','--base-url',env.FILE_OKIBA_BASE_URL,'--group-url',g.url,'--admin-token-env','FILE_OKIBA_ADMIN_TOKEN']);
command(['profile','create','copy','--from','local']);
const redacted=JSON.stringify(command(['profile','list']));assert(!redacted.includes('local-admin-token'));assert(!redacted.includes(g.url));
const upload=command(['upload',input,'--concurrency','2']);assert.equal(upload.completed.length,1);
assert.equal(command(['upload',input]).batchToken,upload.batchToken); // a completed transfer is not duplicated
const links=command(['links',upload.batchUrl]);assert.equal(links.files.length,1);
const output=join(work,'downloads');mkdirSync(output);
const downloaded=command(['download',upload.batchUrl,'--output',output]);assert.deepEqual(readFileSync(downloaded.downloaded[0].path),payload);
const second=command(['download',upload.batchUrl,'--output',output]);assert.notEqual(second.downloaded[0].path,downloaded.downloaded[0].path);
assert.equal(command(['download',upload.batchUrl,'--output',output,'--existing','skip']).skippedExisting.length,1);
command(['group','update',g.id,'--name','renamed']);
command(['group','disable',g.id,'--yes']);command(['group','enable',g.id]);
const fresh=command(['group','rotate-url',g.id,'--yes']);assert(fresh.group.url!==g.url);
assert.equal(command(['group','files','list',g.id]).items.length,1);
command(['group','delete',g.id,'--force','--yes']);
console.log('PASS actual Worker: profiles, multipart upload (2 parts), completed resume, links, download bytes, rename, skip, update, disable/enable, rotate, list, delete');
console.log(`Evidence directory: ${work}`);
