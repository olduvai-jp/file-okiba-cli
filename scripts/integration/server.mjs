// node scripts/integration/server.mjs ../file-okiba CERT_DIR
// Requires the sibling service's installed dev dependencies. Never uses its secrets or databases.
import {createRequire} from 'node:module';
import {resolve,dirname,join} from 'node:path';
import {fileURLToPath} from 'node:url';
import {readFile,readdir} from 'node:fs/promises';
import http from 'node:http';
import https from 'node:https';
const servicePath=resolve(process.argv[2]||'../file-okiba');
const require=createRequire(join(servicePath,'package.json'));
const {Miniflare,convertV4MiniflareOptions}=require('miniflare'); const esbuild=require('esbuild');
const bundle=await esbuild.build({entryPoints:[join(dirname(fileURLToPath(import.meta.url)),'worker.ts')],bundle:true,write:false,format:'esm',platform:'browser',mainFields:['browser','module','main'],external:['node:*','cloudflare:*'],alias:{'service-under-test':join(servicePath,'src/index.ts')}});
const mf=new Miniflare(convertV4MiniflareOptions({modules:true,script:bundle.outputFiles[0].text,compatibilityDate:'2026-06-04',compatibilityFlags:['nodejs_compat'],d1Databases:['DB'],r2Buckets:['FILES_BUCKET'],bindings:{ADMIN_API_TOKEN:'local-admin-token',CREATE_GROUP_SECRET:'local-create-secret',DOWNLOAD_TOKEN_SECRET:'local-download-secret',R2_ACCOUNT_ID:'localtest',R2_BUCKET_NAME:'local-bucket',R2_ACCESS_KEY_ID:'local-access',R2_SECRET_ACCESS_KEY:'local-secret',MAX_UPLOAD_BYTES:'1073741824',DEFAULT_RETENTION_DAYS:'7',MULTIPART_PART_SIZE_BYTES:'5242880',MULTIPART_MAX_CONCURRENCY:'4',MULTIPART_SIGNED_URL_TTL_SECONDS:'900',MULTIPART_SESSION_TTL_HOURS:'24'},serviceBindings:{ASSETS:()=>new Response('local-test')}}));
const db=await mf.getD1Database('DB');
for (const file of (await readdir(join(servicePath,'migrations'))).filter(n=>n.endsWith('.sql')).sort()) {
 const text=await readFile(join(servicePath,'migrations',file),'utf8');
 for(const sql of require('wrangler').unstable_splitSqlQuery(text).map(s=>s.replace(/^\s*--.*$/gm,'').trim()).filter(Boolean)) await db.prepare(sql).run();
}
async function forward(req,res,storage=false){try{const chunks=[];for await(const c of req)chunks.push(c);const body=Buffer.concat(chunks);const target='http://localhost:8788'+(storage?'/__storage':'')+req.url;const response=await mf.dispatchFetch(target,{method:req.method,headers:req.headers,body:body.length?body:undefined});res.writeHead(response.status,Object.fromEntries(response.headers));res.end(Buffer.from(await response.arrayBuffer()));}catch(e){console.error(e);res.writeHead(500);res.end('fixture failure');}}
const gateway=http.createServer((req,res)=>forward(req,res));await new Promise(r=>gateway.listen(8788,'127.0.0.1',r));
let proxy,mitm;
if(process.argv[3]){const certDir=resolve(process.argv[3]);mitm=https.createServer({key:await readFile(join(certDir,'key.pem')),cert:await readFile(join(certDir,'cert.pem'))},(req,res)=>forward(req,res,true));proxy=http.createServer();proxy.on('connect',(req,socket,head)=>{if(req.url!=='localtest.r2.cloudflarestorage.com:443'){socket.end('HTTP/1.1 403 Forbidden\r\n\r\n');return;}socket.write('HTTP/1.1 200 Connection Established\r\n\r\n');if(head.length)socket.unshift(head);mitm.emit('connection',socket);});await new Promise(r=>proxy.listen(8789,'127.0.0.1',r));}
console.log('READY local service=127.0.0.1:8788 storage-proxy=127.0.0.1:8789');
async function stop(){gateway.close();proxy?.close();mitm?.close();await mf.dispose();process.exit(0);}
process.on('SIGINT',stop);process.on('SIGTERM',stop);
