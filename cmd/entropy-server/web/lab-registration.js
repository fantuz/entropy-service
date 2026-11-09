"use strict";

// Configuration preparation stays local. Only an operator can install/reload a
// registry; these controls do not add a remote filesystem or arbitrary-dial API.
(() => {
    const kinds=["webpage","http","websocket","entropy-http","entropy-ws","rtsp","onvif","tcp","udp","unix","device"];
    function build(value){
        const source={id:String(value.id||"").trim(),name:String(value.name||"").trim(),kind:value.kind,endpoint:String(value.endpoint||"").trim(),max_bytes:Number(value.max_bytes),timeout_ms:Number(value.timeout_ms)};
        if(!/^[a-z][a-z0-9_-]{0,31}$/.test(source.id))throw new Error("ID needs 1–32 lowercase letters/digits/_/-, starting with a letter");
        if(new TextEncoder().encode(source.name).length>80||!kinds.includes(source.kind))throw new Error("Invalid kind or name (maximum 80 UTF-8 bytes)");
        if(!Number.isInteger(source.max_bytes)||source.max_bytes<1||source.max_bytes>1048576||!Number.isInteger(source.timeout_ms)||source.timeout_ms<100||source.timeout_ms>60000)throw new Error("Bytes: 1–1,048,576; timeout: 100–60,000 ms");
        function checkURL(raw,schemes){
            let url;try{url=new URL(raw);}catch(_){throw new Error("Enter a complete source URL");}
            if(!schemes.includes(url.protocol)||!url.hostname||url.username||url.password||url.hash||raw.length>2048)throw new Error("Wrong URL scheme, credentials or fragment; enter camera credentials at capture time");
            if(url.pathname==="/lab"||url.pathname.startsWith("/lab/"))throw new Error("Relay endpoints cannot be upstream sources");
            return url;
        }
        if(["unix","device"].includes(source.kind)){
            if(!source.endpoint.startsWith("/")||source.endpoint.length>4096)throw new Error("Use an absolute server-side socket/device path");
        }else if(["tcp","udp"].includes(source.kind)){
            const url=checkURL("tcp://"+source.endpoint,["tcp:"]);
            if(!url.port||url.pathname||url.search)throw new Error("Use host:port, e.g. 127.0.0.1:9001");
        }else{
            const url=checkURL(source.endpoint,source.kind==="rtsp"?["rtsp:"]:source.kind==="websocket"?["ws:","wss:"]:["http:","https:"]);
            if(source.kind.startsWith("entropy-")&&((url.pathname&&url.pathname!=="/")||url.search))throw new Error("Peer registration requires a base HTTP(S) URL without path/query");
        }
        if(source.kind==="webpage"){
            const urls=Array.isArray(value.urls)?value.urls:String(value.urls||"").split(/\r?\n/).map(s=>s.trim()).filter(Boolean);
            if(urls.length>16)throw new Error("Use at most 16 additional page URLs");
            urls.forEach(url=>checkURL(url,["http:","https:"]));if(urls.length)source.urls=urls;
        }
        if(["rtsp","onvif"].includes(source.kind)){
            source.width=Number(value.width)||320;source.height=Number(value.height)||240;
            if(![[160,120],[320,240],[640,480]].some(([w,h])=>source.width===w&&source.height===h))throw new Error("Unsupported camera size");
            source.max_bytes=source.width*source.height*3;
            if(source.kind==="onvif"&&value.profile_token){source.profile_token=String(value.profile_token).trim();if(source.profile_token.length>256)throw new Error("ONVIF profile token exceeds 256 characters");}
        }
        return source;
    }
    function configuration(value){
        const fields=["id","name","kind","endpoint","max_bytes","timeout_ms","urls","width","height","profile_token"];
        if(!value||Object.keys(value).some(key=>key!=="sources")||!Array.isArray(value.sources)||value.sources.length>32)throw new Error("Import a registry with at most 32 sources");
        const sources=value.sources.map(source=>{
            if(!source||Object.keys(source).some(key=>!fields.includes(key)))throw new Error("Unknown source field; credentials do not belong in configuration exports");
            return build({...source,max_bytes:source.max_bytes??1048576,timeout_ms:source.timeout_ms??5000});
        });
        if(new Set(sources.map(source=>source.id)).size!==sources.length)throw new Error("Source IDs must be unique");
        if(new TextEncoder().encode(JSON.stringify({sources},null,2)).length>65536)throw new Error("Configuration exceeds the server's 64 KiB limit");
        return {sources};
    }
    globalThis.EntropyLabRegistration=Object.freeze({build,configuration});
    if(typeof document==="undefined")return;
    const trigger=document.getElementById("lab-create-source");if(!trigger)return;
    const dialog=document.createElement("dialog");dialog.className="lab-registration";dialog.id="lab-registration";
    dialog.innerHTML=`<form id="lab-registration-form"><h2 id="registration-title">Register a source</h2>
      <p class="note">Prepare a server configuration locally. Import an existing registry to append to it. This tool does not activate sources or write files on the server.</p>
      <div class="analysis-controls">
        <label>Source kind<select name="kind">${kinds.map(kind=>`<option>${kind}</option>`).join("")}</select></label>
        <label>ID<input name="id" required pattern="[a-z][a-z0-9_-]{0,31}" placeholder="lab-camera"></label>
        <label>Display name<input name="name" maxlength="80" placeholder="Optional friendly name"></label>
        <label class="registration-wide">Endpoint<input name="endpoint" required placeholder="https://example.org/"></label>
        <label>Maximum sample bytes<input name="max_bytes" type="number" min="1" max="1048576" value="1048576" required></label>
        <label>Timeout (ms)<input name="timeout_ms" type="number" min="100" max="60000" value="10000" required></label>
        <label class="registration-camera" hidden>Decoded size<select name="resolution"><option>160x120</option><option selected>320x240</option><option>640x480</option></select></label>
        <label class="registration-onvif" hidden>ONVIF profile token<input name="profile_token" maxlength="256" placeholder="Empty: first Media profile"></label>
        <label class="registration-pool registration-wide">Additional page URLs, one per line<textarea name="urls" rows="3" placeholder="Optional pool: one URL chosen per sample"></textarea></label>
      </div>
      <p id="registration-help" class="note"></p>
      <div class="lab-actions"><button type="submit">Add registration</button><button type="button" id="registration-close">Close</button></div>
    </form>
    <p id="registration-status" role="status" class="note"></p>
    <label>Import existing registry (local file)<input type="file" id="registration-import" accept=".json,application/json"></label>
    <label>Configuration JSON<textarea id="registration-json" rows="8" readonly spellcheck="false"></textarea></label>
    <div class="lab-actions"><button type="button" id="registration-download">Download lab-sources.json</button><button type="button" id="registration-copy">Copy JSON</button><button type="button" id="registration-reset">New configuration</button></div>
    <p class="note">Save this file on the entropy-server host, then restart with:</p><pre>./bin/entropy-server -lab-sources=lab-sources.json</pre>
    <p class="note">Camera credentials are entered in the capture form for each session. Camera registration needs FFmpeg on the server. ONVIF uses an explicit Device service URL; no LAN scanning is performed.</p>`;
    dialog.setAttribute("aria-labelledby","registration-title");document.body.append(dialog);
    const form=dialog.querySelector("form"),field=name=>form.elements.namedItem(name),status=text=>{dialog.querySelector("#registration-status").textContent=text;};
    let registry={sources:[]};
    function render(){dialog.querySelector("#registration-json").value=JSON.stringify(registry,null,2);dialog.querySelector("#registration-download").disabled=dialog.querySelector("#registration-copy").disabled=!registry.sources.length;}
    function fields(){
        const kind=field("kind").value,camera=["rtsp","onvif"].includes(kind);
        for(const [selector,shown] of [[".registration-camera",camera],[".registration-onvif",kind==="onvif"],[".registration-pool",kind==="webpage"]])dialog.querySelectorAll(selector).forEach(node=>{node.hidden=!shown;node.querySelectorAll("input,select,textarea").forEach(control=>{control.disabled=!shown;});});
        const [width,height]=field("resolution").value.split("x").map(Number);field("max_bytes").disabled=camera;if(camera)field("max_bytes").value=width*height*3;
        dialog.querySelector("#registration-help").textContent=kind==="webpage"?"Reads a body prefix and follows up to two redirects on the same origin. A pool chooses randomly between the endpoint and additional URLs.":kind==="rtsp"?"Use the RTSP URL supplied by your camera; username/password are entered at capture time.":kind==="onvif"?"Use e.g. http://camera/onvif/device_service. Media profiles and the RTSP URI are discovered using ONVIF Media1.":"Endpoint paths/devices belong to the server host. Keep bridges within your trusted lab.";
    }
    trigger.addEventListener("click",()=>{fields();render();dialog.showModal();});dialog.querySelector("#registration-close").addEventListener("click",()=>dialog.close());form.addEventListener("change",fields);
    form.addEventListener("submit",event=>{event.preventDefault();try{
        if(!form.reportValidity())return;
        const values=Object.fromEntries(Array.from(form.elements).filter(control=>control.name).map(control=>[control.name,control.value]));[values.width,values.height]=field("resolution").value.split("x");
        const source=build(values);if(registry.sources.some(item=>item.id===source.id))throw new Error("ID already exists; choose another ID");if(registry.sources.length>=32)throw new Error("The registry is limited to 32 sources");
        registry=configuration({sources:[...registry.sources,source]});render();status(`${source.id} added. Download/install the JSON and restart entropy-server to activate it.`);
    }catch(error){status(error.message);}});
    dialog.querySelector("#registration-import").addEventListener("change",async event=>{try{const file=event.target.files[0];if(!file)return;if(file.size>65536)throw new Error("Registry exceeds 64 KiB");registry=configuration(JSON.parse(await file.text()));render();status(`Imported ${registry.sources.length} registrations locally.`);}catch(error){status(error.message);}});
    dialog.querySelector("#registration-reset").addEventListener("click",()=>{registry={sources:[]};render();status("New empty configuration.");});
    dialog.querySelector("#registration-copy").addEventListener("click",async()=>{try{await navigator.clipboard.writeText(JSON.stringify(registry,null,2));status("Configuration copied.");}catch(_){status("Clipboard unavailable; use Download or select the JSON text.");}});
    dialog.querySelector("#registration-download").addEventListener("click",()=>{const url=URL.createObjectURL(new Blob([JSON.stringify(registry,null,2)],{type:"application/json"})),a=document.createElement("a");a.href=url;a.download="lab-sources.json";a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);});
    render();fields();
})();
