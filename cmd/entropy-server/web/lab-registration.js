"use strict";

// Configuration preparation stays local. Only an operator can install/reload a
// registry; these controls do not add a remote filesystem or arbitrary-dial API.
(() => {
    const kinds=["webpage","http","websocket","entropy-http","entropy-ws","rtsp","onvif","tcp","udp","unix","device"];
    const pagePresets=Object.freeze([
        {label:"Example.org · HTML",url:"https://example.org/",hint:"Ordinary HTML; use a webpage registration if CORS is blocked."},
        {label:"RFC 4086 · randomness",url:"https://www.rfc-editor.org/rfc/rfc4086.txt",hint:"Long plain text; currently grants browser CORS. A registered source is available if site policy changes."},
        {label:"Random Wikipedia page",url:"https://en.wikipedia.org/wiki/Special:Random",hint:"Uses redirects; site policies may require the registered-server route."},
        {label:"HTTPBin · 4,096 binary bytes",url:"https://httpbin.org/bytes/4096",hint:"CORS-enabled binary example; often works in browser mode."},
        {label:"HTTPBin · JSON",url:"https://httpbin.org/json",hint:"CORS-enabled structured JSON comparison data."},
        {label:"Project README · plain text",url:"https://raw.githubusercontent.com/fantuz/entropy-service/main/README.md",hint:"Public source text; normally permits browser CORS."},
        {label:"RFC 1149 · IP over avian carriers",url:"https://www.rfc-editor.org/rfc/rfc1149.txt",hint:"A second plain-text example; currently grants browser CORS."},
    ]);
    function schemaFor(kind){
        if(!kinds.includes(kind))throw new Error("Unknown source kind");
        const schemas={
            webpage:{label:"Web-page HTTP(S) URL",example:"https://example.org/",schemes:["http:","https:"],help:"Reads a prefix; follows up to two same-origin redirects or same-host HTTP→HTTPS upgrades on default ports. Additional URLs form a random pool."},
            http:{label:"Binary HTTP(S) URL",example:"https://httpbin.org/bytes/4096",schemes:["http:","https:"],help:"Strict binary source: the complete body must fit Sample bytes; redirects are rejected. Choose webpage for HTML/text prefixes."},
            websocket:{label:"Complete binary WebSocket URL",example:"ws://127.0.0.1:8081/stream?bytes=4096&refresh=500",schemes:["ws:","wss:"],help:"Use ws://host:port/path or wss://host/path. The upstream must push binary messages without a subscription/request; each must fit Sample bytes. For entropy-server, entropy-ws automatically sets bytes/refresh."},
            "entropy-http":{label:"Entropy-server base HTTP(S) URL",example:"http://127.0.0.1:8081",schemes:["http:","https:"],help:"Enter the base HTTP(S) address, without a path/query. The bridge adds /v1/data/random and sampling parameters."},
            "entropy-ws":{label:"Entropy-server base HTTP(S) URL",example:"http://127.0.0.1:8081",schemes:["http:","https:"],help:"This peer type expects http:// or https://, even though it uses WebSocket. The bridge adds /stream and selected bytes/refresh; choose websocket for a full ws:// URL."},
            rtsp:{label:"Camera RTSP URL",example:"rtsp://192.168.1.100:554/Streaming/Channels/102",schemes:["rtsp:"],help:"Use the camera's RTSP/TCP stream URL without userinfo. Enter camera credentials in the capture form; FFmpeg is required on the server."},
            onvif:{label:"ONVIF Device service HTTP(S) URL",example:"http://192.168.1.100/onvif/device_service",schemes:["http:","https:"],help:"This is the Device service, not the RTSP feed. Media1 discovery obtains the feed; an empty profile token selects the first profile. Enter camera credentials at capture time."},
            tcp:{label:"TCP producer host:port",example:"127.0.0.1:9001",help:"entropy-server connects as a TCP client and reads available raw bytes up to Sample bytes. The producer must listen and send data without waiting for a request; no HTTP/TLS/echo commands are sent."},
            udp:{label:"Local UDP listen address:port",example:"127.0.0.1:9002",help:"entropy-server binds this local address. Another process sends nonempty datagrams to it after Start; this does not pull a remote UDP service. Sample bytes must fit each datagram."},
            unix:{label:"Server-side Unix socket path",example:"/tmp/entropy-source.sock",help:"Connects to a local byte-producing stream socket. No commands are sent."},
            device:{label:"Server-side device/file path",example:"/dev/urandom",help:"Reads a registered Linux byte device/file. This path belongs to entropy-server, not the browser computer."},
        };
        if(!schemas[kind])throw new Error("Unknown source kind");return schemas[kind];
    }
    function httpStatus(code,redirects=0){
        const suffix=redirects===true?" · redirect followed":redirects?` · ${redirects} redirect(s) followed`:"";
        if(code===201)return `HTTP 201 Created${suffix} · success, not a redirect; a Location header identifies a created resource.`;
        return `HTTP ${code}${suffix}`;
    }
    const sourceFields=["id","name","kind","endpoint","max_bytes","timeout_ms","urls","width","height","profile_token"];
    const utf8=value=>new TextEncoder().encode(value).length;
    function invalid(field,message){const error=new Error(message);error.field=field;throw error;}
    // Imported JSON is validated without form coercion or dropping fields. Keep
    // defaults and byte limits in step with internal/lab.Config.validate.
    function validate(value){
        if(!value||Array.isArray(value)||typeof value!=="object"||Object.keys(value).some(key=>!sourceFields.includes(key)))invalid("","Unknown source field; credentials do not belong in configuration exports");
        const source={};
        for(const key of ["id","name","kind","endpoint","profile_token"]){
            if(value[key]!=null&&typeof value[key]!=="string")invalid(key,`${key} must be a string`);
            source[key]=value[key]??"";
        }
        for(const key of ["max_bytes","timeout_ms","width","height"]){
            if(value[key]!=null&&!Number.isSafeInteger(value[key]))invalid(key,`${key} must be an integer`);
            source[key]=value[key]??0;
        }
        if(!/^[a-z][a-z0-9_-]{0,31}$/.test(source.id))invalid("id","ID needs 1–32 lowercase letters/digits/_/-, starting with a letter");
        if(!source.name)source.name=source.id;
        if(utf8(source.name)>80)invalid("name","Name exceeds 80 UTF-8 bytes");
        if(!kinds.includes(source.kind))invalid("kind","Unknown source kind");
        const schema=schemaFor(source.kind),expected=`${schema.label}. Example: ${schema.example}`;
        const camera=["rtsp","onvif"].includes(source.kind);
        if(camera){
            if(!source.width&&!source.height){source.width=320;source.height=240;}
            if(![[160,120],[320,240],[640,480]].some(([w,h])=>source.width===w&&source.height===h))invalid("resolution","Unsupported camera size");
            if(!source.max_bytes)source.max_bytes=source.width*source.height*3;
            if(source.max_bytes!==source.width*source.height*3)invalid("max_bytes","Camera max_bytes must equal the RGB frame size");
        }else if(source.width||source.height||source.profile_token)invalid("kind","Camera fields require an RTSP or ONVIF source");
        if(utf8(source.profile_token)>256||(source.profile_token&&source.kind!=="onvif"))invalid("profile_token","ONVIF profile token must be at most 256 UTF-8 bytes and requires an ONVIF source");
        if(!source.max_bytes)source.max_bytes=33554432;
        if(!source.timeout_ms)source.timeout_ms=camera?10000:5000;
        if(source.max_bytes<1||source.max_bytes>33554432)invalid("max_bytes","Sample bytes must be 1–33,554,432");
        if(source.timeout_ms<100||source.timeout_ms>60000)invalid("timeout_ms","Timeout must be 100–60,000 ms");
        const urls=value.urls??[];
        if(!Array.isArray(urls)||urls.some(url=>typeof url!=="string"))invalid("urls","URL pool must be an array of strings");
        if(urls.length>16||(urls.length&&source.kind!=="webpage"))invalid("urls","Only webpage sources accept a pool of up to 16 URLs");
        function checkURL(raw,schemes,field="endpoint"){
            let url;try{url=new URL(raw);}catch(_){invalid(field,`Enter a complete ${expected}`);}
            if(raw!==raw.trim()||/[\x00-\x20\x7f]/.test(raw)||!schemes.includes(url.protocol)||!url.hostname)invalid(field,`Expected ${expected}`);
            if(url.username||url.password)invalid(field,camera?"Remove camera credentials from the URL; enter them in the Network camera capture form.":`URL credentials are not supported for this source. Use ${schema.example} without userinfo.`);
            if(url.hash)invalid(field,`Remove the URL fragment (#...). ${expected}`);
            if(utf8(raw)>2048)invalid(field,"Source URL exceeds 2,048 UTF-8 bytes");
            // Go decodes and cleans paths before rejecting relay recursion.
            let path;try{path=decodeURIComponent(url.pathname);}catch(_){invalid(field,"Invalid URL path encoding");}
            const parts=[];for(const part of path.split("/")){if(part==="..")parts.pop();else if(part&&part!==".")parts.push(part);}
            if(source.kind!=="rtsp"&&(parts[0]==="lab"))invalid(field,"Relay endpoints cannot be upstream sources");
            return url;
        }
        if(["unix","device"].includes(source.kind)){
            if(!source.endpoint.startsWith("/")||utf8(source.endpoint)>4096)invalid("endpoint",`Use an absolute path of at most 4,096 UTF-8 bytes. ${expected}`);
        }else if(["tcp","udp"].includes(source.kind)){
            let url;try{url=new URL("tcp://"+source.endpoint);}catch(_){invalid("endpoint",`Expected ${expected} (no ${source.kind}:// prefix).`);}
            if(source.endpoint.includes("://")||!url.hostname||!url.port||url.pathname||url.search||url.hash||url.username||url.password||Number(url.port)<1)invalid("endpoint",`Expected ${expected} (no ${source.kind}:// prefix).`);
        }else{
            const url=checkURL(source.endpoint,schema.schemes);
            const rawPath=source.endpoint.match(/^[^:]+:\/\/[^/?#]*([^?#]*)/)?.[1]||"";
            if(source.kind.startsWith("entropy-")&&((rawPath&&rawPath!=="/")||url.search))invalid("endpoint",`Peer registration requires a base HTTP(S) URL without path/query. Example: ${schema.example}`);
        }
        urls.forEach(url=>checkURL(url,["http:","https:"],"urls"));if(urls.length)source.urls=[...urls];
        if(!camera){delete source.width;delete source.height;}
        if(!source.profile_token)delete source.profile_token;
        return source;
    }
    function build(value){
        const source={id:String(value.id||"").trim(),name:String(value.name||"").trim(),kind:value.kind,endpoint:String(value.endpoint||"").trim(),max_bytes:Number(value.max_bytes??0),timeout_ms:Number(value.timeout_ms??0)};
        if(source.kind==="webpage")source.urls=Array.isArray(value.urls)?value.urls:String(value.urls||"").split(/\r?\n/).map(s=>s.trim()).filter(Boolean);
        if(["rtsp","onvif"].includes(source.kind)){
            source.width=Number(value.width??0);source.height=Number(value.height??0);
            if(source.kind==="onvif")source.profile_token=String(value.profile_token||"").trim();
        }
        return validate(source);
    }
    function configuration(value){
        if(!value||Object.keys(value).some(key=>key!=="sources")||!Array.isArray(value.sources)||value.sources.length>32)throw new Error("Import a registry with at most 32 sources");
        const sources=value.sources.map(validate);
        if(new Set(sources.map(source=>source.id)).size!==sources.length)throw new Error("Source IDs must be unique");
        if(new TextEncoder().encode(JSON.stringify({sources},null,2)).length>65536)throw new Error("Configuration exceeds the server's 64 KiB limit");
        return {sources};
    }
    globalThis.EntropyLabRegistration=Object.freeze({build,configuration,schemaFor,pagePresets,httpStatus});
    if(typeof document==="undefined")return;
    const trigger=document.getElementById("lab-create-source");if(!trigger)return;
    const t = (message, values = {}) => window.EntropyI18n?.t(message, values) ??
        message.replace(/\{(\w+)\}/g, (match, key) => Object.hasOwn(values, key) ? String(values[key]) : match);
    function setText(node, message, values = {}) {
        if (window.EntropyI18n) window.EntropyI18n.set(node, message, values);
        else node.textContent = t(message, values);
    }
    function setAttribute(node, name, message, values = {}) {
        if (window.EntropyI18n) window.EntropyI18n.attr(node, name, message, values);
        else node.setAttribute(name, t(message, values));
    }

    const dialog=document.createElement("dialog");dialog.className="lab-registration";dialog.id="lab-registration";
    dialog.innerHTML=`<header class="registration-header"><div><h2 id="registration-title" class="analysis-label" data-i18n="Prepare source registry">Prepare source registry</h2><p id="registration-draft-state" class="note"></p></div><button type="button" id="registration-close" data-i18n="Close">Close</button></header>
    <div class="registration-body">
      <p class="note" data-i18n="Prepare a server configuration locally. Import an existing registry to append to it. This tool does not activate sources or write files on the server. Capture also obeys the server’s -max-bytes and lab memory limits; browser samples are capped at 8 MiB.">Prepare a server configuration locally. Import an existing registry to append to it. This tool does not activate sources or write files on the server. Capture also obeys the server’s -max-bytes and lab memory limits; browser samples are capped at 8 MiB.</p>
      <section aria-labelledby="registration-list-title"><h3 id="registration-list-title" class="analysis-label" data-i18n="Draft sources">Draft sources</h3><ul id="registration-list" class="registration-list"></ul></section>
      <p id="registration-status" role="status" class="note"></p>
      <form id="lab-registration-form">
      <div class="analysis-controls">
        <label><span data-i18n="Source kind">Source kind</span><select name="kind">${kinds.map(kind=>`<option>${kind}</option>`).join("")}</select></label>
        <label><span data-i18n="ID">ID</span><input name="id" required pattern="[a-z][a-z0-9_-]{0,31}" placeholder="lab-camera"></label>
        <label><span data-i18n="Display name">Display name</span><input name="name" maxlength="80" placeholder="Optional friendly name" data-i18n-placeholder="Optional friendly name"></label>
        <label class="registration-wide"><span id="registration-endpoint-label" data-i18n="Endpoint">Endpoint</span><input name="endpoint" required aria-describedby="registration-example registration-help" placeholder="https://example.org/"></label>
        <div class="registration-wide"><p id="registration-example" class="note"></p><button type="button" id="registration-use-example" data-i18n="Use example">Use example</button></div>
        <label class="registration-pages registration-wide" hidden><span data-i18n="Web-page example">Web-page example</span><select id="registration-page-example"><option value="" data-i18n="Choose an example…">Choose an example…</option>${pagePresets.map(p=>`<option value="${p.url}">${p.label}</option>`).join("")}</select></label>
        <label><span data-i18n="Maximum sample bytes">Maximum sample bytes</span><input name="max_bytes" type="number" min="1" max="33554432" value="33554432" required></label>
        <label><span data-i18n="Timeout (ms)">Timeout (ms)</span><input name="timeout_ms" type="number" min="100" max="60000" value="5000" required></label>
        <label class="registration-camera" hidden><span data-i18n="Decoded size">Decoded size</span><select name="resolution"><option>160x120</option><option selected>320x240</option><option>640x480</option></select></label>
        <label class="registration-onvif" hidden><span data-i18n="ONVIF profile token">ONVIF profile token</span><input name="profile_token" maxlength="256" placeholder="Empty: first Media profile" data-i18n-placeholder="Empty: first Media profile"></label>
        <label class="registration-pool registration-wide"><span data-i18n="Additional page URLs, one per line">Additional page URLs, one per line</span><textarea name="urls" rows="3" placeholder="Optional pool: one URL chosen per sample" data-i18n-placeholder="Optional pool: one URL chosen per sample"></textarea></label>
      </div>
      <p id="registration-help" class="note"></p>
      <div class="lab-actions"><button type="submit" id="registration-submit" data-i18n="Add registration">Add registration</button><button type="button" id="registration-cancel-edit" data-i18n="Clear form">Clear form</button></div>
    </form>
    <label><span data-i18n="Import existing registry (local file)">Import existing registry (local file)</span><input type="file" id="registration-import" accept=".json,application/json"></label>
    <details><summary data-i18n="Configuration JSON">Configuration JSON</summary><label><span data-i18n="Local draft">Local draft</span><textarea id="registration-json" rows="8" readonly spellcheck="false"></textarea></label></details>
    <p class="note" data-i18n="Save this file on the entropy-server host, then restart with:">Save this file on the entropy-server host, then restart with:</p><pre>./bin/entropy-server -lab-sources=lab-sources.json</pre>
    <p class="note" id="registration-camera-note" hidden data-i18n="Camera credentials are entered in the capture form for each session. Camera registration needs FFmpeg on the server. ONVIF uses an explicit Device service URL; no LAN scanning is performed.">Camera credentials are entered in the capture form for each session. Camera registration needs FFmpeg on the server. ONVIF uses an explicit Device service URL; no LAN scanning is performed.</p>
    </div><footer class="registration-footer"><div class="lab-actions"><button type="button" id="registration-download" data-i18n="Download JSON">Download JSON</button><button type="button" id="registration-copy" data-i18n="Copy JSON">Copy JSON</button><button type="button" id="registration-reset" data-i18n="New configuration">New configuration</button></div></footer>`;
    dialog.setAttribute("aria-labelledby","registration-title");document.body.append(dialog);
    window.EntropyI18n?.translate(dialog);
    dialog.querySelectorAll("#registration-page-example option").forEach((option,index)=>{if(index)setText(option,pagePresets[index-1].label);});
    const form=dialog.querySelector("form"),field=name=>form.elements.namedItem(name),status=(text,values={})=>{setText(dialog.querySelector("#registration-status"),text,values);};
    const json=()=>JSON.stringify(registry,null,2);
    let registry={sources:[]},saved=json(),formDirty=false,editing=null,revision=0,leavingGuard=false;
    let previousKind=field("kind").value,previousBytes=field("max_bytes").value;
    const dirty=()=>formDirty||json()!==saved;
    function beforeUnload(event){if(dirty()){event.preventDefault();event.returnValue="";}}
    function draftState(){
        const unsaved=dirty();
        setText(dialog.querySelector("#registration-draft-state"),registry.sources.length===1?(unsaved?"{count} registration · Unsaved changes":"{count} registration · No pending changes"):(unsaved?"{count} registrations · Unsaved changes":"{count} registrations · No pending changes"),{count:registry.sources.length});
        if(unsaved!==leavingGuard){window[unsaved?"addEventListener":"removeEventListener"]("beforeunload",beforeUnload);leavingGuard=unsaved;}
    }
    function clearErrors(){
        form.querySelectorAll("[name]").forEach(control=>{control.removeAttribute("aria-invalid");control.setCustomValidity("");});
        form.querySelectorAll(".field-error").forEach(node=>{node.hidden=true;setText(node,"");});
    }
    function showError(error){
        status(error.message);
        const control=error.field&&field(error.field);
        if(control&&!control.disabled){
            control.setAttribute("aria-invalid","true");
            const feedback=form.querySelector(`#registration-error-${control.name}`);setText(feedback,error.message);feedback.hidden=false;
            control.focus();control.scrollIntoView({block:"center"});
        }
    }
    form.querySelectorAll("[name]").forEach(control=>{
        const feedback=document.createElement("span");feedback.id=`registration-error-${control.name}`;feedback.className="field-error";feedback.hidden=true;
        control.closest("label").append(feedback);control.setAttribute("aria-describedby",`${control.getAttribute("aria-describedby")||""} ${feedback.id}`.trim());
    });
    function resetForm(){
        form.reset();editing=null;formDirty=false;clearErrors();previousKind=field("kind").value;previousBytes=field("max_bytes").value;fields();
        setText(dialog.querySelector("#registration-submit"),"Add registration");setText(dialog.querySelector("#registration-cancel-edit"),"Clear form");
    }
    function render(){
        dialog.querySelector("#registration-json").value=json();
        dialog.querySelector("#registration-download").disabled=dialog.querySelector("#registration-copy").disabled=!registry.sources.length;
        const list=dialog.querySelector("#registration-list");list.replaceChildren();
        if(!registry.sources.length){const item=document.createElement("li");item.className="note";setText(item,"No draft sources. Add one below or import a registry.");list.append(item);}
        for(const source of registry.sources){
            const item=document.createElement("li"),text=document.createElement("span"),actions=document.createElement("div");
            text.textContent=`${source.name} · ${source.id} · ${source.kind}`;actions.className="registration-entry-actions";
            for(const action of ["Edit","Remove"]){
                const button=document.createElement("button");button.type="button";setText(button,action);setAttribute(button,"aria-label",action+" {id}",{id:source.id});
                button.addEventListener("click",()=>{
                    if(action==="Edit"){
                        if(formDirty&&!window.confirm(t("Discard the unfinished form edits?")))return;
                        revision++;resetForm();
                        for(const key of ["id","name","kind","endpoint","max_bytes","timeout_ms","profile_token"])field(key).value=source[key]??"";
                        if(source.width)field("resolution").value=`${source.width}x${source.height}`;
                        field("urls").value=(source.urls||[]).join("\n");fields(true);editing=source.id;
                        setText(dialog.querySelector("#registration-submit"),"Save registration");setText(dialog.querySelector("#registration-cancel-edit"),"Cancel edit");
                        status("Editing {id}; Save registration applies your changes to the draft.",{id:source.id});field("id").focus();
                    }else{
                        revision++;registry={sources:registry.sources.filter(item=>item.id!==source.id)};
                        if(editing===source.id)resetForm();render();status("{id} removed from the draft.",{id:source.id});
                    }
                    draftState();
                });actions.append(button);
            }
            item.append(text,actions);list.append(item);
        }
        draftState();
    }
    function fields(preserve=false){
        const kind=field("kind").value,camera=["rtsp","onvif"].includes(kind);
        for(const [selector,shown] of [[".registration-camera",camera],[".registration-onvif",kind==="onvif"],[".registration-pool",kind==="webpage"],[".registration-pages",["webpage","http"].includes(kind)]])dialog.querySelectorAll(selector).forEach(node=>{node.hidden=!shown;node.querySelectorAll("input,select,textarea").forEach(control=>{control.disabled=!shown;});});
        const wasCamera=["rtsp","onvif"].includes(previousKind);
        if(kind!==previousKind){
            if(!wasCamera)previousBytes=field("max_bytes").value;
            if(!preserve&&field("timeout_ms").value===(wasCamera?"10000":"5000"))field("timeout_ms").value=camera?"10000":"5000";
            if(!preserve&&wasCamera&&!camera)field("max_bytes").value=previousBytes;
        }
        const [width,height]=field("resolution").value.split("x").map(Number);field("max_bytes").disabled=camera;if(camera)field("max_bytes").value=width*height*3;
        previousKind=kind;
        const schema=schemaFor(kind);field("endpoint").placeholder=schema.example;
        setText(dialog.querySelector("#registration-endpoint-label"),schema.label);
        setText(dialog.querySelector("#registration-example"),"Example: {example}",{example:schema.example});
        setText(dialog.querySelector("#registration-help"),schema.help);
        dialog.querySelector("#registration-camera-note").hidden=!camera;
    }
    function changed(){revision++;formDirty=true;clearErrors();draftState();}
    dialog.querySelector("#registration-use-example").addEventListener("click",()=>{field("endpoint").value=schemaFor(field("kind").value).example;changed();status("Example filled; adjust the host/port/path for your source.");});
    dialog.querySelector("#registration-page-example").addEventListener("change",event=>{if(event.target.value){field("endpoint").value=event.target.value;status(pagePresets.find(p=>p.url===event.target.value).hint);}});
    trigger.addEventListener("click",()=>{fields();render();dialog.showModal();});dialog.querySelector("#registration-close").addEventListener("click",()=>dialog.close());
    form.addEventListener("input",changed);form.addEventListener("change",()=>{fields();changed();});
    form.addEventListener("invalid",event=>showError({field:event.target.name,message:event.target.validationMessage}),true);
    dialog.querySelector("#registration-cancel-edit").addEventListener("click",()=>{revision++;resetForm();draftState();status("Form edits discarded; draft registrations kept.");});
    form.addEventListener("submit",event=>{event.preventDefault();try{
        clearErrors();
        if(!form.reportValidity())return;
        const values=Object.fromEntries(Array.from(form.elements).filter(control=>control.name&&!control.disabled).map(control=>[control.name,control.value]));
        if(["rtsp","onvif"].includes(values.kind)){[values.width,values.height]=field("resolution").value.split("x");values.max_bytes=Number(values.width)*Number(values.height)*3;}
        const source=build(values);
        if(registry.sources.some(item=>item.id===source.id&&item.id!==editing))invalid("id","ID already exists; choose another ID");
        const sources=editing?registry.sources.map(item=>item.id===editing?source:item):[...registry.sources,source];
        registry=configuration({sources});revision++;resetForm();render();status("{id} saved in the draft; not active yet. Download/install the JSON and restart entropy-server with -lab-sources=lab-sources.json.",{id:source.id});
    }catch(error){showError(error);}});
    dialog.querySelector("#registration-import").addEventListener("change",async event=>{
        const file=event.target.files[0];event.target.value="";if(!file)return;
        const token=++revision;
        if(dirty()&&!window.confirm(t("Replace the unsaved draft and unfinished form with this registry?")))return;
        status("Reading local registry…");
        try{
            if(file.size>65536)throw new Error("Registry exceeds 64 KiB");
            const text=await file.text();if(token!==revision)return;
            const imported=configuration(JSON.parse(text));registry=imported;resetForm();saved=json();render();status("Imported {count} registrations locally.",{count:registry.sources.length});
        }catch(error){if(token===revision)status(error.message);}
    });
    dialog.querySelector("#registration-reset").addEventListener("click",()=>{
        if(dirty()&&!window.confirm(t("Discard the unsaved draft and start a new configuration?")))return;
        revision++;registry={sources:[]};resetForm();saved=json();render();status("New empty configuration.");
    });
    dialog.querySelector("#registration-copy").addEventListener("click",async()=>{
        const text=json(),token=revision;try{await navigator.clipboard.writeText(text);if(token!==revision)return;saved=text;draftState();status("Configuration copied; install it on the server to activate sources.");}catch(_){if(token===revision)status("Clipboard unavailable; use Download or select the JSON text.");}
    });
    dialog.querySelector("#registration-download").addEventListener("click",()=>{
        const text=json(),url=URL.createObjectURL(new Blob([text],{type:"application/json"})),a=document.createElement("a");a.href=url;a.download="lab-sources.json";a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
        saved=text;draftState();status("Download requested; install the JSON on the server to activate sources.");
    });
    window.addEventListener("entropylanguagechange",draftState);
    render();fields();
})();
