// Run only against an explicitly selected ARM Linux test device.
// Requires server.mjs locally, SSH/SCP, and sha256sum+dd on the device.
import {spawnSync} from 'node:child_process';
import {mkdtempSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {resolve,join} from 'node:path';
import assert from 'node:assert/strict';
const host=process.argv[2],binary=process.argv[3];if(!host||!binary)throw new Error('Usage: node scripts/integration/arm-check.mjs SSH_HOST ARM64_BINARY');
const q=s=>"'"+String(s).replaceAll("'","'\\''")+"'";
function exec(cmd,args){const r=spawnSync(cmd,args,{encoding:'utf8'});assert.equal(r.status,0,r.stderr);return r.stdout;}
function ssh(cmd){return exec('ssh',['-o','BatchMode=yes',host,cmd]);}
const dir=ssh('mktemp -d /tmp/file-okiba-cli-test.XXXXXX').trim();assert(/^\/tmp\/file-okiba-cli-test\.[A-Za-z0-9]+$/.test(dir));
const local=mkdtempSync(join(tmpdir(),'file-okiba-ssh-')),control=join(local,'control');let tunnel=false;
try{
 exec('scp',['-O',resolve(binary),`${host}:${dir}/file-okiba`]);exec('scp',['-O',resolve('.local/test/tls/cert.pem'),`${host}:${dir}/cert.pem`]);
 exec('ssh',['-o','BatchMode=yes','-o','ExitOnForwardFailure=yes','-M','-S',control,'-fN','-R','18788:127.0.0.1:8788','-R','18789:127.0.0.1:8789',host]);tunnel=true;
 ssh('chmod 700 '+q(dir+'/file-okiba'));
 const prefix=`env FILE_OKIBA_CONFIG=${q(dir+'/config.json')} FILE_OKIBA_STATE_DIR=${q(dir+'/state')} FILE_OKIBA_BASE_URL=http://localhost:18788 FILE_OKIBA_ADMIN_TOKEN=local-admin-token HTTPS_PROXY=http://localhost:18789 SSL_CERT_FILE=${q(dir+'/cert.pem')} ${q(dir+'/file-okiba')}`;
 function cli(args,timed=false){const command=prefix+' '+args.map(q).join(' ')+' --json';return JSON.parse(ssh(timed?'/usr/bin/time -v -o '+q(dir+'/time.txt')+' '+command:command));}
 const g=cli(['group','create','--name','ARM64 CLI test']).group;const url=new URL(g.url);url.port='18788';
 ssh(`dd if=/dev/urandom of=${q(dir+'/input.bin')} bs=1048576 count=6 2>/dev/null`);
 cli(['profile','create','arm','--base-url','http://localhost:18788','--group-url',url.href]);
 const u=cli(['upload',dir+'/input.bin','--concurrency','2'],true);assert.equal(u.completed.length,1);
 const d=cli(['download',u.batchUrl,'--output',dir+'/download']);assert.equal(d.downloaded.length,1);
 const sums=ssh('sha256sum '+q(dir+'/input.bin')+' '+q(d.downloaded[0].path)).trim().split('\n').map(x=>x.split(/\s/)[0]);assert.equal(sums[0],sums[1]);
 assert.equal(cli(['upload',dir+'/input.bin']).batchToken,u.batchToken);
 cli(['group','delete',g.id,'--force','--yes']);
 console.log('PASS '+ssh('uname -sm').trim()+': production binary, 6 MiB multipart upload/download, SHA-256 match, completed resume, admin delete');console.log('SHA-256: '+sums[0]);console.log(ssh('cat '+q(dir+'/time.txt')));
}finally{if(tunnel)spawnSync('ssh',['-S',control,'-O','exit',host]);ssh('rm -rf '+q(dir));rmSync(local,{recursive:true,force:true});}
