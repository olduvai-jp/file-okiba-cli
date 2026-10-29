// Test-only adapter: runs the actual sibling Worker with local D1/R2.
// Only S3 transport is replaced; no request can reach production storage.
import service from 'service-under-test';
let currentEnv: any;
const uploads = new Map<string, { key: string; parts: Map<number, ArrayBuffer> }>();
function keyFromURL(url: URL) { return decodeURIComponent(url.pathname.split('/').slice(2).join('/')); }
globalThis.fetch = async (input: any, init: any = {}) => {
 const url = new URL(typeof input === 'string' ? input : input.url);
 if (url.hostname !== 'localtest.r2.cloudflarestorage.com') throw new Error('External networking disabled in integration test');
 const id = url.searchParams.get('uploadId');
 if (init.method === 'POST' && url.searchParams.has('uploads')) {
  const uploadId = crypto.randomUUID(); uploads.set(uploadId, { key: keyFromURL(url), parts: new Map() });
  return new Response(`<InitiateMultipartUploadResult><UploadId>${uploadId}</UploadId></InitiateMultipartUploadResult>`);
 }
 if (init.method === 'DELETE') { uploads.delete(id!); return new Response(''); }
 if (init.method === 'POST' && id) {
  const upload = uploads.get(id)!;
  const parts = [...upload.parts].sort((a,b) => a[0]-b[0]).map(p=>new Uint8Array(p[1]));
  const bytes = new Uint8Array(parts.reduce((n,p)=>n+p.byteLength,0)); let offset=0;
  for (const p of parts) { bytes.set(p,offset); offset+=p.byteLength; }
  await currentEnv.FILES_BUCKET.put(upload.key,bytes); uploads.delete(id);
  return new Response('<CompleteMultipartUploadResult />');
 }
 throw new Error('Unexpected S3 operation');
};
export default {
 async fetch(request: Request, env: any, ctx: any) {
  currentEnv=env; const url=new URL(request.url);
  if (!url.pathname.startsWith('/__storage/')) return service.fetch(request,env,ctx);
  const storageURL=new URL(url.pathname.slice('/__storage'.length)+url.search,'https://localtest.r2.cloudflarestorage.com');
  if (request.method==='PUT') {
   const id=storageURL.searchParams.get('uploadId')!; const n=Number(storageURL.searchParams.get('partNumber'));
   const upload=uploads.get(id); if (!upload) return new Response('',{status:404});
   const bytes=await request.arrayBuffer(); upload.parts.set(n,bytes);
   const digest=await crypto.subtle.digest('SHA-256',bytes);
   const etag='"'+[...new Uint8Array(digest)].map(n=>n.toString(16).padStart(2,'0')).join('')+'"';
   return new Response('',{headers:{ETag:etag}});
  }
  const object=await env.FILES_BUCKET.get(keyFromURL(storageURL));
  if (!object) return new Response('',{status:404});
  const all=new Uint8Array(await object.arrayBuffer());const etag='"'+object.etag+'"';
  const range=request.headers.get('Range');
  if (range && request.headers.get('If-Range')===etag) {
   const start=Number(/^bytes=(\d+)-$/.exec(range)?.[1]);
   return new Response(all.slice(start),{status:206,headers:{ETag:etag,'Content-Range':`bytes ${start}-${all.length-1}/${all.length}`}});
  }
  return new Response(all,{headers:{ETag:etag,'Content-Length':String(all.length)}});
 }
};
