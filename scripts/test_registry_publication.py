#!/usr/bin/env python3
"""Exercise exact OCI publication against a real loopback registry with faults.

Requires Docker, Skopeo and ORAS. Inputs must be the actual CI-tested archives,
including scan evidence. Never publishes to GHCR or changes the latest channel.
"""
import argparse
from contextlib import contextmanager
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import subprocess
import socket
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid

from oci_release import native_inputs, verify

# Distribution's official registry image; update deliberately with maintenance.
REGISTRY = 'registry:3@sha256:82f6ff4a7f982960c8aa51617fea3aabc485df6e55cce80d22deb1de36f1bc3c'


def docker(*args):
    result=subprocess.run(['docker',*args],stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,timeout=90)
    if result.returncode:raise RuntimeError(result.stdout)
    return result.stdout.strip()


@contextmanager
def registry():
    name='entropy-registry-'+uuid.uuid4().hex[:12]
    try:
        # Ask the host kernel, since rootless Docker's automatic allocator may
        # otherwise choose a port already used by the fault proxy/client.
        with socket.socket() as reservation:
            reservation.bind(('127.0.0.1',0));host_port=reservation.getsockname()[1]
        docker('run','--detach','--name',name,'--publish',f'127.0.0.1:{host_port}:5000',REGISTRY)
        address=json.loads(docker('inspect',name))[0]['NetworkSettings']['Ports']['5000/tcp'][0]
        port=int(address['HostPort'])
        until=time.monotonic()+20
        while True:
            try:
                with urllib.request.urlopen(f'http://127.0.0.1:{port}/v2/',timeout=1) as response:
                    assert response.status==200
                break
            except (OSError,urllib.error.URLError):
                if time.monotonic()>until:raise
                time.sleep(.1)
        yield port
    finally:
        docker('rm','--force',name)


class FaultProxy(BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'

    def log_message(self,*args):pass

    def handle(self):
        try:super().handle()
        except (ConnectionResetError,BrokenPipeError):pass

    def body(self):
        if self.headers.get('Transfer-Encoding','').lower()=='chunked':
            data=bytearray()
            while True:
                size=int(self.rfile.readline().split(b';',1)[0],16)
                if not size:
                    while self.rfile.readline()!=b'\r\n':pass
                    return bytes(data)
                if len(data)+size>128*1024*1024:raise ValueError('oversized registry request')
                data.extend(self.rfile.read(size));assert self.rfile.read(2)==b'\r\n'
        size=int(self.headers.get('Content-Length','0'))
        if not 0<=size<=128*1024*1024:raise ValueError('oversized registry request')
        return self.rfile.read(size)

    def forward(self):
        body=self.body()
        state=self.server.state
        target=self.command=='PUT' and self.path==state['target']
        if target:
            with state['lock']:
                state['stage_puts']+=1
                fail=state['fault'] and (state['fault']=='denied' or state['stage_puts']==1)
            if fail:
                status,code,message={'unavailable':(503,'UNAVAILABLE','service unavailable'),
                                     'blob':(400,'MANIFEST_BLOB_UNKNOWN','blob unknown to registry'),
                                     'denied':(403,'DENIED','denied: controlled permanent registry error')}[state['fault']]
                data=json.dumps({'errors':[{'code':code,'message':message}]}).encode()
                self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
                return
        headers={k:v for k,v in self.headers.items() if k.lower() not in ('connection','transfer-encoding','content-length','expect')}
        if self.headers.get_all('Accept'):
            headers['Accept']=', '.join(self.headers.get_all('Accept'))
        headers['Content-Length']=str(len(body))
        connection=http.client.HTTPConnection('127.0.0.1',state['port'],timeout=30)
        try:
            connection.request(self.command,self.path,body,headers)
            response=connection.getresponse();data=response.read()
            self.send_response(response.status)
            for key,value in response.getheaders():
                if key.lower() not in ('connection','transfer-encoding','content-length','server','date'):self.send_header(key,value)
            self.send_header('Content-Length',str(len(data)));self.end_headers()
            if self.command!='HEAD':self.wfile.write(data)
        finally:connection.close()

    do_GET=do_HEAD=do_POST=do_PUT=do_PATCH=do_DELETE=forward


@contextmanager
def proxy(port,fault,target):
    server=ThreadingHTTPServer(('127.0.0.1',0),FaultProxy)
    server.daemon_threads=True
    server.state={'port':port,'fault':fault,'stage_puts':0,'lock':threading.Lock(),'target':target}
    thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
    try:yield server
    finally:server.shutdown();server.server_close();thread.join(timeout=5)


def manifest(port,repository,reference):
    connection=http.client.HTTPConnection('127.0.0.1',port,timeout=10)
    try:
        connection.request('GET',f'/v2/{repository}/manifests/{reference}',headers={'Accept':'application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json'})
        response=connection.getresponse();return response.status,response.read()
    finally:connection.close()


def exercise(args):
    native=native_inputs(args.artifacts,args.source_sha,args.version,args.repository)
    bundles=verify(args.artifacts/'container-oci-bundles',args.source_sha,args.version,args.repository,native)
    tag='edge' if args.version=='edge' else 'v'+args.version
    owner=args.repository.lower().split('/',1)[0]
    repositories={'server':args.repository.lower(),'client':owner+'/entropy-client'}
    results=[]
    with tempfile.TemporaryDirectory(prefix='entropy-registry-auth-') as temporary:
        auth=Path(temporary)/'auth.json';auth.write_text('{"auths":{}}');auth.chmod(0o600)
        for fault in ('baseline','unavailable','blob','denied'):
            with registry() as port,proxy(port,None if fault=='baseline' else fault,f'/v2/{repositories["client"]}/manifests/stage-{tag}') as server:
                target=f'127.0.0.1:{server.server_port}'
                command=[sys.executable,str(Path(__file__).with_name('publish_containers.py')),'--repository',args.repository,
                         '--source-sha',args.source_sha,'--tag',tag,'--artifacts',str(args.artifacts),
                         '--registry',target,'--local-registry','--authfile',str(auth)]
                result=subprocess.run(command,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=240)
                text=result.stdout.decode(errors='replace')
                print(f'[{fault}] exit={result.returncode}\n{text}',flush=True)
                if fault=='denied':
                    assert result.returncode!=0 and server.state['stage_puts']==1,text
                    assert 'Transient registry failure' not in text,text
                    for repository in repositories.values():
                        assert manifest(port,repository,tag)[0]==404,'Public channel changed after staging failure'
                        assert manifest(port,repository,tag+'-linux-amd64')[0]==404,'Platform alias changed after staging failure'
                else:
                    assert result.returncode==0,text
                    assert server.state['stage_puts']==(1 if fault=='baseline' else 2),server.state
                    if fault!='baseline':assert 'Transient registry failure; retry 2/4' in text,text
                    for role,repository in repositories.items():
                        status,raw=manifest(port,repository,tag)
                        assert status==200 and 'sha256:'+hashlib.sha256(raw).hexdigest()==bundles['images'][role]['digest']
                        for platform,image in bundles['images'][role]['platforms'].items():
                            status,raw=manifest(port,repository,tag+'-'+platform)
                            assert status==200 and 'sha256:'+hashlib.sha256(raw).hexdigest()==image['digest']
                    if fault=='baseline':
                        repeated=subprocess.run(command,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=240)
                        assert repeated.returncode==0,repeated.stdout.decode(errors='replace')
                        # Re-publishing remains digest-identical, including root indexes.
                        for role,repository in repositories.items():
                            status,raw=manifest(port,repository,tag)
                            assert status==200 and 'sha256:'+hashlib.sha256(raw).hexdigest()==bundles['images'][role]['digest']
                results.append({'case':fault,'stage_puts':server.state['stage_puts'],'passed':True})
    args.evidence.parent.mkdir(parents=True,exist_ok=True)
    args.evidence.write_text(json.dumps({'source_sha':args.source_sha,'registry':REGISTRY,'cases':results},indent=2)+'\n')
    print('Real registry publication, idempotency, transient retries and permanent failure checks passed')


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--artifacts',type=Path,required=True)
    parser.add_argument('--source-sha',required=True)
    parser.add_argument('--version',default='edge')
    parser.add_argument('--repository',default='fantuz/entropy-service')
    parser.add_argument('--evidence',type=Path,default=Path('registry-evidence.json'))
    exercise(parser.parse_args())


if __name__=='__main__':main()
