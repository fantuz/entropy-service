"use strict";

(() => {
    const MAX_BYTES = 8 * 1024 * 1024;
    function pattern(text) {
        const normalized = text.replace(/\s+/g, "");
        if (!/^(?:[0-9a-f]{2}){1,512}$/i.test(normalized)) throw new Error("Pattern needs 1–512 complete hexadecimal bytes");
        return Uint8Array.from(normalized.match(/../g), pair => parseInt(pair,16));
    }
    function generate(kind,size,{value = 0,sequence = 0,bytes = Uint8Array.of(0,255),crypto = globalThis.crypto} = {}) {
        if (!Number.isInteger(size) || size < 1 || size > MAX_BYTES) throw new Error("Sample size must be 1–8,388,608 bytes");
        const output = new Uint8Array(size);
        if (kind === "crypto") {
            if (!crypto?.getRandomValues) throw new Error("Web Crypto is unavailable");
            for (let i=0;i<size;i+=65536) crypto.getRandomValues(output.subarray(i,Math.min(size,i+65536)));
        } else if (kind === "constant") {
            if (!Number.isInteger(value) || value<0 || value>255) throw new Error("Constant must be 0–255");
            output.fill(value);
        } else if (kind === "counter") {
            for (let i=0;i<size;i++) output[i]=(sequence+i)&255;
        } else if (kind === "pattern") {
            if (!bytes.length) throw new Error("Empty pattern");
            for (let i=0;i<size;i++) output[i]=bytes[(sequence+i)%bytes.length];
        } else throw new Error("Unknown generator");
        return output;
    }
    function transform(bytes,mode,previous) {
        if (mode === "raw") return bytes;
        if (mode === "lsb") return bytes.map(value => value&1 ? 255 : 0);
        if (mode === "xor") {
            if (!previous || previous.length!==bytes.length) return null;
            return bytes.map((value,i) => value^previous[i]);
        }
        throw new Error("Unknown transformation");
    }
    class Meter {
        constructor() {this.reset(0);}
        reset(now) {this.start=now;this.end=null;this.bytes=0;this.frames=0;this.last=null;this.intervalSum=0;}
        add(bytes,now) {
            this.bytes+=bytes;this.frames++;
            if(this.last!==null) this.intervalSum+=Math.max(0,now-this.last);
            this.last=now;
        }
        stop(now) {if(this.end===null)this.end=now;}
        snapshot(now) {
            const elapsedMs=Math.max(0,(this.end??now)-this.start);
            return {bytes:this.bytes,frames:this.frames,elapsedMs,
                bytesPerSecond:this.frames>1&&elapsedMs>0?this.bytes*1000/elapsedMs:null,
                meanIntervalMs:this.frames>1?this.intervalSum/(this.frames-1):null};
        }
    }
    function endpoint(raw,kind) {
        const url=new URL(raw,location.href);
        const schemes=kind==="http"?["http:","https:"]:["ws:","wss:"];
        if(!schemes.includes(url.protocol)||url.username||url.password||url.hash) throw new Error("Use a valid HTTP(S) or WS(S) URL without credentials or fragment");
        if(location.protocol==="https:"&&["http:","ws:"].includes(url.protocol)) throw new Error("An HTTPS page requires an HTTPS/WSS source");
        return url;
    }
    globalThis.EntropyLabSamples=Object.freeze({MAX_BYTES,pattern,generate,transform,Meter,endpoint});
})();
