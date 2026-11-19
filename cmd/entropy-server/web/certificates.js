"use strict";

(() => {
    const $=id=>document.getElementById(id), crypto=window.EntropyCertificateCrypto;
    const text=(node,message,values={})=>window.EntropyI18n?window.EntropyI18n.set(node,message,values):node.textContent=message;
    let generated=null,pending=null,generation=0;
    const urls=new Set();
    function busy(value){$("certificate-generate").disabled=value;$("certificate-cancel").disabled=!value;}
    function clear(status=true){
        generation++;pending?.abort();pending=null;generated=null;
        for(const url of urls)URL.revokeObjectURL(url);urls.clear();
        $("certificate-result").hidden=true;$("certificate-pem").value="";
        for(const id of ["names","before","after","fingerprint"])$("certificate-result-"+id).textContent="";
        busy(false);
        if(status){$("certificate-status").dataset.state="ready";text($("certificate-status"),"Generated material cleared from this page.");}
    }
    async function entropy(signal){
        const response=await fetch("/v1/data/random?bytes=64",{signal,credentials:"omit",redirect:"error",cache:"no-store"});
        if(!response.ok)throw Object.assign(new Error("Server entropy request failed (HTTP {status})"),{values:{status:response.status}});
        if(!response.body)throw new Error("Server entropy response is unavailable");
        const reader=response.body.getReader(),bytes=new Uint8Array(64);let count=0;
        try{
            while(true){const part=await reader.read();if(part.done)break;if(count+part.value.length>64)throw new Error("Server entropy response exceeds 64 bytes");bytes.set(part.value,count);count+=part.value.length;}
            if(count!==64)throw new Error("Server entropy response must contain exactly 64 bytes");
            return bytes;
        }catch(error){bytes.fill(0);await reader.cancel().catch(()=>{});throw error;}
        finally{reader.releaseLock();}
    }
    function download(name,pem){
        const url=URL.createObjectURL(new Blob([pem],{type:"application/x-pem-file"}));urls.add(url);
        const link=document.createElement("a");link.href=url;link.download=name;link.hidden=true;
        document.body.append(link);link.click();link.remove();
        setTimeout(()=>{URL.revokeObjectURL(url);urls.delete(url);},30000);
    }
    $("certificate-form").addEventListener("submit",async event=>{
        event.preventDefault();clear(false);const serial=generation,controller=new AbortController();pending=controller;busy(true);
        $("certificate-status").dataset.state="working";text($("certificate-status"),"Generating locally with server and browser randomness…");
        const timer=setTimeout(()=>controller.abort(),10000);let bytes;
        try{
            if(!window.isSecureContext||!window.crypto?.subtle)throw new Error("Certificate generation requires Web Crypto on HTTPS or localhost");
            const options={commonName:$("certificate-common-name").value,days:Number($("certificate-days").value),
                names:$("certificate-names").value.split(/[,\n]+/).map(v=>v.trim()).filter(Boolean)};
            crypto.names(options.names);
            bytes=await entropy(controller.signal);
            clearTimeout(timer);
            const result=await crypto.generate(bytes,options);
            if(serial!==generation||controller.signal.aborted)return;
            generated=result;
            $("certificate-result-names").textContent=result.names.join(", ");
            $("certificate-result-before").textContent=result.notBefore;
            $("certificate-result-after").textContent=result.notAfter;
            $("certificate-result-fingerprint").textContent=result.fingerprint;
            $("certificate-pem").value=crypto.pem("CERTIFICATE",result.certificate);
            $("certificate-result").hidden=false;
            $("certificate-status").dataset.state="ready";text($("certificate-status"),"Certificate ready. Download the certificate and private key separately; neither was uploaded.");
        }catch(error){
            if(serial===generation){$("certificate-status").dataset.state="error";text($("certificate-status"),controller.signal.aborted?"Generation cancelled or the entropy request timed out.":String(error.message),error.values);}
        }finally{bytes?.fill(0);clearTimeout(timer);if(serial===generation){pending=null;busy(false);}}
    });
    $("certificate-clear").addEventListener("click",()=>clear());
    $("certificate-cancel").addEventListener("click",()=>clear());
    $("certificate-download-public").addEventListener("click",()=>{if(generated)download("entropy-server-cert.pem",crypto.pem("CERTIFICATE",generated.certificate));});
    $("certificate-download-key").addEventListener("click",async()=>{
        const result=generated;if(!result)return;let bytes;
        try{bytes=new Uint8Array(await window.crypto.subtle.exportKey("pkcs8",result.privateKey));if(generated===result)download("entropy-server-key.pem",crypto.pem("PRIVATE KEY",bytes));}
        catch(_){$("certificate-status").dataset.state="error";text($("certificate-status"),"The private key could not be exported by this browser.");}
        finally{bytes?.fill(0);}
    });
    window.addEventListener("pagehide",()=>clear(false));
    window.addEventListener("pageshow",event=>{if(event.persisted)clear();});
})();
