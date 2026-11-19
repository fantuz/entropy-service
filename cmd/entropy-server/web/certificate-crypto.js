"use strict";

// HKDF, private scalar multiplication and ECDSA use native Web Crypto.
// The compatibility path decompresses a PUBLIC ECDH x-coordinate only;
// JavaScript BigInt never receives a private scalar or signing nonce.
(() => {
    const encoder=new TextEncoder(), order=hex("ffffffff00000000ffffffffffffffffbce6faada7179e84f3b9cac2fc632551");
    const prime=0xffffffff00000001000000000000000000000000ffffffffffffffffffffffffn;
    const coefficient=0x5ac635d8aa3a93e7b3ebbd55769886bc651d06b0cc53b0f63bce3c3e27d2604bn;
    const generator=hex("046b17d1f2e12c4247f8bce6e563a440f277037d812deb33a0f4a13945d898c296"+
        "4fe342e2fe1a7f9b8ee7eb4a7c0f9e162bce33576b315ececbb6406837bf51f5");
    function hex(value){return Uint8Array.from(value.match(/../g)||[],v=>parseInt(v,16));}
    function join(...values){const out=new Uint8Array(values.reduce((n,v)=>n+v.length,0));let offset=0;for(const value of values){out.set(value,offset);offset+=value.length;}return out;}
    function der(tag,body){
        const n=body.length;
        if(n>65535)throw new Error("Certificate structure exceeds its size limit");
        const length=n<128?Uint8Array.of(n):n<256?Uint8Array.of(0x81,n):Uint8Array.of(0x82,n>>8,n&255);
        return join(Uint8Array.of(tag),length,body);
    }
    const sequence=(...v)=>der(0x30,join(...v)), oid=value=>der(6,hex(value));
    function integer(bytes){
        let start=0;while(start<bytes.length-1&&bytes[start]===0)start++;
        const value=bytes.subarray(start);
        return der(2,value[0]&128?join(Uint8Array.of(0),value):value);
    }
    const signatureAlgorithm=sequence(oid("2a8648ce3d040302"));
    function scalarAllowed(bytes){
        if(bytes.length!==32||!bytes.some(v=>v!==0))return false;
        for(let i=0;i<32;i++){if(bytes[i]<order[i])return true;if(bytes[i]>order[i])return false;}
        return false;
    }
    function privateEncoding(scalar,publicPoint){
        // Track every intermediate DER copy containing the private scalar.
        // The returned encoding belongs to the caller and is cleared there.
        const sensitive=[], keep=bytes=>{sensitive.push(bytes);return bytes;};
        try{
            const point=publicPoint?[der(0xa1,der(3,join(Uint8Array.of(0),publicPoint)))]:[];
            const ec=keep(der(0x30,keep(join(integer(Uint8Array.of(1)),keep(der(4,scalar)),...point))));
            return der(0x30,keep(join(integer(Uint8Array.of(0)),
                sequence(oid("2a8648ce3d0201"),oid("2a8648ce3d030107")),keep(der(4,ec)))));
        }finally{for(const bytes of sensitive)bytes.fill(0);}
    }
    function publicPower(value,exponent){
        let result=1n;
        while(exponent>0n){
            if(exponent&1n)result=result*value%prime;
            value=value*value%prime;exponent>>=1n;
        }
        return result;
    }
    async function nativePublicPoint(crypto,key,encoding,scalar){
        // Firefox can import a scalar-only ECPrivateKey but cannot export its
        // missing public point as JWK. ECDH(d, G) supplies x(dG) natively.
        const privateKey=await crypto.subtle.importKey("pkcs8",encoding,{name:"ECDH",namedCurve:"P-256"},false,["deriveBits"]);
        const base=await crypto.subtle.importKey("raw",generator,{name:"ECDH",namedCurve:"P-256"},false,[]);
        const xBytes=new Uint8Array(await crypto.subtle.deriveBits({name:"ECDH",public:base},privateKey,256));
        if(xBytes.length!==32)throw new Error("Invalid native public coordinate");
        const x=BigInt("0x"+[...xBytes].map(v=>v.toString(16).padStart(2,"0")).join(""));
        if(x>=prime)throw new Error("Invalid native public coordinate");
        // SEC 1 point decompression uses public field elements only. P-256's
        // p is 3 mod 4, so a square root is rhs^((p+1)/4) mod p.
        const rhs=((x*x%prime*x-3n*x+coefficient)%prime+prime)%prime;
        const y=publicPower(rhs,(prime+1n)/4n);
        if(y===0n||y*y%prime!==rhs)throw new Error("Invalid native public coordinate");
        const proofData=encoder.encode("entropy-service:certificate-public-point:v1");
        const proof=await crypto.subtle.sign({name:"ECDSA",hash:"SHA-256"},key,proofData);
        if(proof.byteLength!==64)throw new Error("Invalid native public-point proof");
        const matches=[];
        // An x-coordinate has two candidate points. Ask native ECDSA to
        // identify exactly one; never guess a parity or accept ambiguity.
        for(const candidate of [y,prime-y]){
            const point=join(Uint8Array.of(4),xBytes,hex(candidate.toString(16).padStart(64,"0")));
            const publicKey=await crypto.subtle.importKey("raw",point,{name:"ECDSA",namedCurve:"P-256"},true,["verify"]);
            if(await crypto.subtle.verify({name:"ECDSA",hash:"SHA-256"},publicKey,proof,proofData))matches.push({point,publicKey});
        }
        if(matches.length!==1)throw new Error("Native public-point proof is missing or ambiguous");
        let complete;
        try{
            complete=privateEncoding(scalar,matches[0].point);
            const completeKey=await crypto.subtle.importKey("pkcs8",complete,{name:"ECDSA",namedCurve:"P-256"},true,["sign"]);
            return {key:completeKey,publicKey:matches[0].publicKey};
        }finally{complete?.fill(0);}
    }
    function nativeCrypto(provider){
        if(!provider?.subtle||typeof provider.getRandomValues!=="function")throw new Error("Certificate generation requires Web Crypto on HTTPS or localhost");
        return provider;
    }
    async function identity(serverBytes,provider){
        if(!(serverBytes instanceof Uint8Array)||serverBytes.length!==64)throw new Error("Exactly 64 server bytes are required");
        const crypto=nativeCrypto(provider), local=crypto.getRandomValues(new Uint8Array(64));
        let scalar,encoding;
        try{
            const input=await crypto.subtle.importKey("raw",local,"HKDF",false,["deriveBits"]);
            const derive=(info,bits)=>crypto.subtle.deriveBits({name:"HKDF",hash:"SHA-256",salt:serverBytes,info:encoder.encode(info)},input,bits);
            let key;
            for(let attempt=0;attempt<128;attempt++){
                scalar=new Uint8Array(await derive(`entropy-service:certificate-key:v1:${attempt}`,256));
                if(!scalarAllowed(scalar)){scalar.fill(0);continue;}
                encoding=privateEncoding(scalar);
                try{key=await crypto.subtle.importKey("pkcs8",encoding,{name:"ECDSA",namedCurve:"P-256"},true,["sign"]);}
                catch(_){throw new Error("This browser cannot import the required native P-256 key format; use the CLI certificate generator");}
                break;
            }
            if(!key)throw new Error("Could not derive a valid P-256 key");
            let publicKey;
            try{
                const jwk=await crypto.subtle.exportKey("jwk",key);
                delete jwk.d;
                jwk.key_ops=["verify"];
                publicKey=await crypto.subtle.importKey("jwk",jwk,{name:"ECDSA",namedCurve:"P-256"},true,["verify"]);
            }catch(_){
                try{
                    const compatible=await nativePublicPoint(crypto,key,encoding,scalar);
                    key=compatible.key;publicKey=compatible.publicKey;
                }catch(_){throw new Error("This browser cannot import the required native P-256 key format; use the CLI certificate generator");}
            }
            const serial=new Uint8Array(await derive("entropy-service:certificate-serial:v1",160));
            serial[0]&=0x7f;
            if(!serial.some(v=>v!==0))serial[serial.length-1]=1;
            return {key,publicKey,serial};
        }finally{local.fill(0);scalar?.fill(0);encoding?.fill(0);}
    }
    function ipv4(text){
        const parts=text.split(".");
        if(parts.length!==4||parts.some(p=>!/^(0|[1-9][0-9]{0,2})$/.test(p)||Number(p)>255))throw new Error("Invalid IPv4 address");
        return Uint8Array.from(parts,Number);
    }
    function host(value){
        const raw=value.trim().replace(/\.$/,"");
        if(!raw||raw.length>253||/[\x00-\x20\x7f]/.test(raw))throw new Error("Each certificate name must be a bounded DNS name or IP address");
        if(/^[0-9.]+$/.test(raw))return {name:raw,tag:0x87,bytes:ipv4(raw)};
        if(raw.includes(":")){
            if(!/^(?:[0-9a-f:.]+|\[[0-9a-f:.]+\])$/i.test(raw))throw new Error("Enter an IPv6 address without port, URL or zone");
            let url;try{url=new URL("http://"+(raw.startsWith("[")?raw:`[${raw}]`));}catch(_){throw new Error("Invalid IPv6 address");}
            if(url.port||url.pathname!=="/"||url.username||url.password||url.search||url.hash)throw new Error("Enter an IPv6 address without port, URL or zone");
            const name=url.hostname.replace(/^\[|\]$/g,""), halves=name.split("::");
            if(halves.length>2||name.includes("%"))throw new Error("Invalid IPv6 address");
            const left=halves[0]?halves[0].split(":"):[],right=halves[1]?halves[1].split(":"):[];
            const zeros=halves.length===2?Array(8-left.length-right.length).fill("0"):[];
            const parts=[...left,...zeros,...right];
            if(parts.length!==8||parts.some(p=>!/^([0-9a-f]{1,4})$/i.test(p)))throw new Error("Invalid IPv6 address");
            const bytes=new Uint8Array(16);parts.forEach((p,i)=>{const n=parseInt(p,16);bytes[i*2]=n>>8;bytes[i*2+1]=n&255;});
            return {name,tag:0x87,bytes};
        }
        const wildcard=raw.startsWith("*.");
        if(/[\\/@?#%\[\]]/.test(raw))throw new Error("Enter DNS names without URLs, paths or ports");
        let url;try{url=new URL("http://"+(wildcard?raw.slice(2):raw));}catch(_){throw new Error("Invalid DNS name");}
        const name=(wildcard?"*.":"")+url.hostname.toLowerCase();
        if(url.port||url.pathname!=="/"||url.username||url.password||url.search||url.hash||name.length>253||/^[0-9.]+$/.test(url.hostname)||
            url.hostname.split(".").some(p=>!/^([a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)$/.test(p)))throw new Error("Enter DNS names without URLs, paths or ports");
        return {name,tag:0x82,bytes:encoder.encode(name)};
    }
    function names(values){
        if(!Array.isArray(values)||values.length<1||values.length>32||values.some(v=>typeof v!=="string"))throw new Error("Provide 1–32 DNS names or IP addresses");
        const result=[],seen=new Set();
        for(const value of values){const entry=host(value),key=entry.tag+":"+entry.name;if(!seen.has(key)){seen.add(key);result.push(entry);}}
        return result;
    }
    function time(value){
        const year=value.getUTCFullYear(),text=value.toISOString().replace(/[-:]/g,"").replace("T","").slice(0,14)+"Z";
        return der(year>=1950&&year<2050?0x17:0x18,encoder.encode(year>=1950&&year<2050?text.slice(2):text));
    }
    function extension(id,value,critical=false){return sequence(oid(id),...(critical?[der(1,Uint8Array.of(255))]:[]),der(4,value));}
    function signature(raw){
        const bytes=new Uint8Array(raw);
        if(bytes.length!==64)throw new Error("Unexpected native P-256 signature format");
        return sequence(integer(bytes.subarray(0,32)),integer(bytes.subarray(32)));
    }
    async function generate(serverBytes,options={}){
        const crypto=nativeCrypto(options.crypto||globalThis.crypto), entries=names(options.names||["localhost","127.0.0.1","::1"]);
        const days=Number(options.days??7),now=new Date(options.now??Date.now());
        if(!Number.isInteger(days)||days<1||days>365)throw new Error("Certificate validity must be 1–365 days");
        if(!Number.isFinite(now.getTime())||now.getUTCFullYear()<2020||now.getUTCFullYear()>2098)throw new Error("Check the computer clock before creating a certificate");
        // X.509 time has whole-second precision; display exactly what is signed.
        now.setUTCMilliseconds(0);
        const common=String(options.commonName||entries[0].name).trim();
        if(!common||encoder.encode(common).length>64||/[\x00-\x1f\x7f]/.test(common))throw new Error("Common name must be 1–64 UTF-8 bytes without control characters");
        const subject=sequence(der(0x31,sequence(oid("550403"),der(0x0c,encoder.encode(common)))));
        const material=await identity(serverBytes,crypto);
        const spki=new Uint8Array(await crypto.subtle.exportKey("spki",material.publicKey));
        const before=new Date(now.getTime()-300000),after=new Date(now.getTime()+days*86400000);
        const extensions=sequence(
            extension("551d13",sequence(),true),
            extension("551d0f",der(3,Uint8Array.of(7,0x80)),true),
            extension("551d25",sequence(oid("2b06010505070301"))),
            extension("551d11",sequence(...entries.map(e=>der(e.tag,e.bytes)))));
        const tbs=sequence(der(0xa0,integer(Uint8Array.of(2))),integer(material.serial),signatureAlgorithm,subject,
            sequence(time(before),time(after)),subject,spki,der(0xa3,extensions));
        const signed=signature(await crypto.subtle.sign({name:"ECDSA",hash:"SHA-256"},material.key,tbs));
        const certificate=sequence(tbs,signatureAlgorithm,der(3,join(Uint8Array.of(0),signed)));
        const fingerprint=new Uint8Array(await crypto.subtle.digest("SHA-256",certificate));
        return {certificate,privateKey:material.key,names:entries.map(e=>e.name),notBefore:before.toISOString(),notAfter:after.toISOString(),
            fingerprint:[...fingerprint].map(v=>v.toString(16).padStart(2,"0").toUpperCase()).join(":")};
    }
    function pem(label,bytes){
        if(!/^(CERTIFICATE|PRIVATE KEY)$/.test(label))throw new Error("Unsupported PEM type");
        let raw="";for(const byte of bytes)raw+=String.fromCharCode(byte);
        const encoded=btoa(raw).match(/.{1,64}/g)?.join("\n")||"";
        return `-----BEGIN ${label}-----\n${encoded}\n-----END ${label}-----\n`;
    }
    const api=Object.freeze({generate,pem,names});
    if(typeof module!=="undefined"&&module.exports)module.exports=api;
    else globalThis.EntropyCertificateCrypto=api;
})();
