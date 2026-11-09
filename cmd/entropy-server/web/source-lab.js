"use strict";

(() => {
    const $=id=>document.getElementById(id), helpers=window.EntropyLabSamples;
    const guidance=window.EntropyLabRegistration;
    function browserExample(kind){const example=guidance.schemaFor(kind).example;return location.protocol==="https:"?example.replace(/^http:/,"https:").replace(/^ws:/,"wss:"):example;}
    function endpoint(raw,protocol,kind){try{return helpers.endpoint(raw,protocol);}catch(error){throw new Error(`${error.message}. Example: ${browserExample(kind)}`);}}
    if($("lab-page-preset"))for(const preset of guidance.pagePresets){const option=document.createElement("option");option.value=preset.url;option.textContent=preset.label;$("lab-page-preset").append(option);}
    const meter=new helpers.Meter(), storageKey="entropy-service:lab:measurements:v1", records=[];
    try {
        const raw=sessionStorage.getItem(storageKey);
        if(raw&&raw.length<=131072){
            const saved=JSON.parse(raw);
            if(Array.isArray(saved))records.push(...saved.filter(record=>typeof record?.source==="string"&&typeof record?.representation==="string"&&
                Number.isSafeInteger(record?.window?.bytes)&&record.window.bytes>0&&record.window.bytes<=helpers.MAX_BYTES&&
                Number.isFinite(record?.statistics?.entropy)&&record.statistics.entropy>=0&&record.statistics.entropy<=8&&
                (record?.meter?.bytesPerSecond===null||Number.isFinite(record?.meter?.bytesPerSecond))).slice(-50));
        }
    }catch(_){/* Restricted storage leaves measurements usable in memory. */}
    let generation=0,controller=null,cleanup=null,running=false,paused=false,once=false,finishing=false;
    let options=null,lastInput=null,previous=null,lastAccepted=-Infinity,finalFrame=0,frame=0,analysedBytes=0,analysedFrames=0;
    let meterScheduled=false,lastSubmittedFrame=0,catalog={sources:[],allowed:false,max_bytes:helpers.MAX_BYTES};
    function status(state,text){$("lab-status").dataset.state=state;if($("lab-status").textContent!==text)$("lab-status").textContent=text;}
    function buttons(){
        $("lab-start").disabled=$("lab-once").disabled=running;
        $("lab-stop").disabled=!running;
        $("pause-plots").disabled=!running||once;
        $("pause-plots").textContent=!running?"Plots frozen":paused?"Resume plots":"Pause plots";
        $("pause-plots").setAttribute("aria-pressed",String(paused||!running));
        $("analysis-size").disabled=$("analysis-offset").disabled=running&&paused;
        const have=workspace.sample?.sourceId===generation;
        $("lab-record").disabled=$("lab-download").disabled=!have;
        $("lab-export").disabled=!records.length;
    }
    function updateMeters(){
        if(meterScheduled)return;meterScheduled=true;
        requestAnimationFrame(()=>{
            meterScheduled=false;const data=meter.snapshot(performance.now());
            $("lab-input-bytes").textContent=data.bytes.toLocaleString();$("lab-input-frames").textContent=data.frames.toLocaleString();
            $("lab-input-unit").textContent=data.frames===1?"sample":"samples";
            $("lab-rate").textContent=data.bytesPerSecond===null?"—":(data.bytesPerSecond/1e6).toFixed(4);
            $("lab-interval").textContent=data.meanIntervalMs===null?"—":data.meanIntervalMs.toFixed(2);
            $("lab-analysis-bytes").textContent=analysedBytes.toLocaleString();$("lab-analysis-frames").textContent=analysedFrames.toLocaleString();
            $("lab-analysis-unit").textContent=analysedFrames===1?"window":"windows";
        });
    }
    const workspace=window.EntropyWorkspace({kind:"stream",
        describe:data=>`${data.sourceName} · ${data.representation} · sample #${data.frame.toLocaleString()} · [${data.offset.toLocaleString()}, ${(data.offset+data.analyzedBytes).toLocaleString()}) of ${data.totalBytes.toLocaleString()} bytes · worker ${data.analysisMs.toFixed(1)} ms`,
        onAnalyzed(data){if(data.sourceId===generation){analysedFrames++;analysedBytes+=data.analyzedBytes;updateMeters();}},
        onRender(data){
            if(data.sourceId!==generation)return;
            if(finishing&&data.frame===finalFrame){finishing=false;workspace.setFrozen(true);status("ready","Sample captured · source released");}
            buttons();
        },
        onError(message){stop("error",message);},
        onSelectionChange(){workspace.clear("Window changed · sample again to analyse the new selection.");lastAccepted=-Infinity;buttons();}
    });
    function stopTransport(){controller?.abort();controller=null;const close=cleanup;cleanup=null;if(close)close();if(options)options.credentials=null;meter.stop(performance.now());running=false;updateMeters();}
    function stop(state="disconnected",message="Source stopped · plots frozen"){
        finishing=false;stopTransport();workspace.setFrozen(true);paused=false;status(state,message);buttons();
    }
    function finish(message){
        const wasPaused=paused;stopTransport();paused=false;
        if(wasPaused){workspace.setFrozen(true);status("disconnected",message+" · plots frozen");}
        else if(lastInput){
            finalFrame=lastInput.frame;
            if(workspace.sample?.frame===finalFrame&&workspace.sample.sourceId===generation){workspace.setFrozen(true);status("ready",message+" · source released");}
            else{finishing=true;if(lastSubmittedFrame!==finalFrame)submit(lastInput,true);status("waiting",message+" · finishing analysis");}
        }
        else{workspace.setFrozen(true);status("disconnected",message+" · no complete represented sample");}
        buttons();
    }
    function submit(sample,force=false){
        if(!sample)return;
        const now=performance.now();
        if(!force&&(paused||document.hidden||now-lastAccepted<1000/Number($("display-fps").value)))return;
        const offset=Number($("analysis-offset").value);
        if(!Number.isSafeInteger(offset)||offset<0||offset>=sample.bytes.length){
            if(force){finishing=false;workspace.setFrozen(true);}status("error","Start byte must be within the represented sample");return;
        }
        lastAccepted=now;lastSubmittedFrame=sample.frame;
        workspace.submit(sample.bytes.buffer.slice(sample.bytes.byteOffset+offset,sample.bytes.byteOffset+Math.min(sample.bytes.length,offset+Number($("analysis-size").value))),
            {totalBytes:sample.bytes.length,offset,sourceId:generation,frame:sample.frame,time:sample.time,captureTime:sample.captureTime,
                sourceName:options.label,representation:options.transform,encoding:sample.encoding});
    }
    function capture(bytes,encoding,token){
        if(token!==generation||!controller||controller.signal.aborted)return;
        if(!(bytes instanceof Uint8Array)||!bytes.length||bytes.length>helpers.MAX_BYTES){stop("error","Source sample must contain 1–1,048,576 bytes");return;}
        const now=performance.now();meter.add(bytes.length,now);frame++;
        const output=helpers.transform(bytes,options.transform,previous);
        previous=options.transform==="xor"?bytes.slice():null;
        $("lab-source-info").textContent=`${options.label} · ${encoding} · representation: ${options.transform}`;
        if($("lab-body-preview")&&(options.selection==="http"||options.registeredKind==="webpage"))$("lab-body-preview").textContent=new TextDecoder().decode(bytes.subarray(0,2048));
        updateMeters();
        if(!output){status("waiting","XOR warming up · needs two equal-length samples");return;}
        lastInput={bytes:output,encoding,frame,time:Date.now(),captureTime:now};
        status("ready",paused?"Receiving · plots paused":"Source active");
        if(once){finish("One sample collected");return;}
        submit(lastInput);
    }
    function readOptions(){
        if(!$("lab-config").reportValidity())return null;
        const selected=$("lab-kind").value,size=Number($("lab-bytes").value),refresh=Number($("lab-refresh").value);
        const result={kind:selected,selection:selected,size,refresh,cadenceApplied:!["websocket","serial"].includes(selected),transform:$("lab-transform").value,label:$("lab-kind").selectedOptions[0].textContent,
            value:Number($("lab-constant")?.value||0),pattern:selected==="pattern"?helpers.pattern($("lab-pattern").value):Uint8Array.of(0,255),
            file:$("lab-file")?.files[0],resolution:$("lab-resolution")?.value||"320x240",format:$("lab-format")?.value||"rgb",baud:Number($("lab-baud")?.value||115200)};
        if(selected==="self-ws"||selected==="self-http"||selected==="peer-ws"||selected==="peer-http"){
            const base=selected.startsWith("self")?new URL(location.href):endpoint($("lab-url").value,"http",selected==="peer-ws"?"entropy-ws":"entropy-http");
            const websocket=selected.endsWith("ws");
            const url=new URL(websocket?"/stream":"/v1/data/random",base);
            url.searchParams.set("bytes",size);url.searchParams.set("refresh",refresh);
            if(websocket)url.protocol=url.protocol==="https:"?"wss:":"ws:";
            result.kind=websocket?"websocket":"http";result.url=url.href;result.label+=` · ${base.host}`;
        }else if(selected==="bridge"||selected==="rtsp"||(selected==="http"&&$("lab-http-route")?.value==="registered")){
            const source=catalog.sources.find(value=>value.id===$("lab-registered").value);
            if(!source)throw new Error("No active matching source: save the registry on the server and restart with -lab-sources=lab-sources.json");
            if(!catalog.allowed)throw new Error("Bridge access is restricted: use a loopback client or the operator's -lab-allow-remote configuration");
            if(size>Math.min(source.max_bytes,catalog.max_bytes))throw new Error("Sample exceeds the registered source byte limit");
            const url=new URL("/lab/stream",location.href);url.protocol=location.protocol==="https:"?"wss:":"ws:";
            url.searchParams.set("source",source.id);url.searchParams.set("bytes",size);url.searchParams.set("refresh",refresh);
            url.searchParams.set("events","1");
            result.kind="websocket";result.events=true;result.registeredId=source.id;result.registeredKind=source.kind;result.url=url.href;result.label=`Registered ${source.name} (${source.kind})`;
            if(selected==="rtsp"){
                if(!source.width)throw new Error("Choose a registered RTSP/ONVIF camera");
                result.camera={width:source.width,height:source.height};result.preview=$("lab-show-preview").checked;
                result.credentials={username:$("lab-camera-user").value,password:$("lab-camera-password").value};
                if(result.credentials.password&&!result.credentials.username)throw new Error("Enter a camera username with the password");
                if((result.credentials.username||result.credentials.password)&&location.protocol!=="https:"&&!["localhost","127.0.0.1","[::1]"].includes(location.hostname))throw new Error("Use HTTPS to send camera credentials to a remote entropy-server");
                if(result.preview&&source.width*source.height*3>catalog.max_bytes)throw new Error("Preview exceeds server byte limit; choose a smaller registered camera size or disable preview");
                $("lab-camera-password").value="";
                url.searchParams.set("preview",result.preview?"1":"0");result.url=url.href;
            }
        }else if(selected==="http"||selected==="websocket"){
            const url=endpoint($("lab-url").value,selected,selected);result.url=url.href;result.label+=` · ${url.host}`;
        }
        return result;
    }
    async function start(single=false){
        let requested;try{requested=readOptions();}catch(error){status("error",error.message);return;}
        if(!requested)return;
        stopTransport();generation++;options=requested;once=single;finishing=false;paused=false;running=true;
        $("lab-transport-info").textContent="";
        if($("lab-body-preview"))$("lab-body-preview").textContent="";
        lastInput=null;previous=null;frame=0;analysedBytes=0;analysedFrames=0;lastAccepted=-Infinity;lastSubmittedFrame=0;
        meter.reset(performance.now());workspace.clear();workspace.setFrozen(false);controller=new AbortController();
        const signal=controller.signal,token=generation;
        status("waiting","Connecting or awaiting source permission…");buttons();updateMeters();
        try{
            const close=await window.EntropyLabAdapters.start(options,{signal,video:$("lab-video"),
                info(message){if(token===generation&&!signal.aborted)$("lab-transport-info").textContent=message;},
                previewEnabled:()=>$("lab-show-preview").checked,previewCanvas:$("lab-network-preview"),
                ready(){if(token===generation&&!signal.aborted){meter.reset(performance.now());status("waiting","Source ready · awaiting sample");}},
                emit:(bytes,encoding)=>capture(bytes,encoding,token),
                end:message=>{if(token===generation&&!signal.aborted)finish(message);},
                error:error=>{if(token===generation&&!signal.aborted)stop("error",error.message);}
            });
            if(token!==generation||signal.aborted)close?.();else cleanup=close;
        }catch(error){if(token===generation&&!signal.aborted)stop("error",error.message);}
    }
    function sourceFields(){
        const kind=$("lab-kind").value;
        const serverHTTP=kind==="http"&&$("lab-http-route")?.value==="registered";
        if($("lab-registered")){
            const select=$("lab-registered"),selected=select.value;
            const available=catalog.sources.filter(source=>document.body.dataset.lab==="sources"?source.width>0:serverHTTP?["http","webpage","entropy-http"].includes(source.kind):!source.width);
            if(select.dataset.ids!==available.map(source=>source.id).join("|")){
                select.replaceChildren();
                for(const source of available){const option=document.createElement("option");option.value=source.id;option.textContent=`${source.name} (${source.kind})`;select.append(option);}
                if(!available.length){const option=document.createElement("option");option.value="";option.textContent="No matching registrations";select.append(option);}
                if(available.some(source=>source.id===selected))select.value=selected;
                select.dataset.ids=available.map(source=>source.id).join("|");
            }
        }
        document.querySelectorAll("[data-modes]").forEach(label=>{
            label.hidden=!label.dataset.modes.split(" ").includes(kind);
            if(label.querySelector("#lab-url"))label.hidden=label.hidden||serverHTTP;
            if(label.querySelector("#lab-registered")&&kind==="http")label.hidden=!serverHTTP;
            if(label.querySelector("#lab-page-preset"))label.hidden=label.hidden||serverHTTP;
            label.querySelectorAll("input,select").forEach(control=>{control.disabled=label.hidden;});
        });
        if($("lab-url"))$("lab-url").required=["peer-ws","peer-http","http","websocket"].includes(kind)&&!serverHTTP;
        if($("lab-url-label")){
            const sourceKind=kind==="websocket"?"websocket":kind==="peer-ws"?"entropy-ws":kind==="peer-http"?"entropy-http":"webpage",schema=guidance.schemaFor(sourceKind);
            $("lab-url-label").textContent=schema.label;$("lab-url").placeholder=browserExample(sourceKind);
        }
        if($("lab-file"))$("lab-file").required=kind==="file";
        $("lab-preview-card").hidden=!["camera","rtsp"].includes(kind);
        const source=catalog.sources.find(value=>value.id===$("lab-registered")?.value);
        if($("lab-route-help")){
            let hint="";
            if(kind==="http"){
                if(serverHTTP)hint=source?"Server route: reads a registered endpoint using entropy-server's network access. "+guidance.schemaFor(source.kind).help:"No HTTP sources are active. Download/import a registry, restart with -lab-sources=lab-sources.json, then select its source ID here.";
                else hint="Browser route: the target must allow CORS; this page cannot grant that permission. Try HTTPBin or the public README examples. For ordinary pages, register a webpage source and restart with -lab-sources=lab-sources.json.";
                if(!serverHTTP){const preset=guidance.pagePresets.find(p=>p.url===$("lab-page-preset").value);if(preset)hint=preset.hint+" "+hint;}
            }else if(kind==="bridge"&&source)hint=guidance.schemaFor(source.kind).help;
            else if(["websocket","peer-ws","peer-http"].includes(kind))hint=guidance.schemaFor(kind==="websocket"?kind:kind==="peer-ws"?"entropy-ws":"entropy-http").help;
            $("lab-route-help").textContent=hint;
        }
        if($("lab-body-card"))$("lab-body-card").hidden=kind!=="http"&&!(kind==="bridge"&&source?.kind==="webpage");
        const limit=kind==="bridge"||kind==="rtsp"||serverHTTP?Math.min(catalog.max_bytes,source?.max_bytes||helpers.MAX_BYTES):helpers.MAX_BYTES;
        $("lab-bytes").max=limit;if(Number($("lab-bytes").value)>limit)$("lab-bytes").value=limit;
    }
    $("lab-config").addEventListener("submit",event=>{event.preventDefault();void start(false);});
    $("lab-show-preview").addEventListener("change",()=>{
        if($("lab-kind").value==="rtsp"&&running)stop("disconnected","Preview changed · Start to apply");
        $("lab-video").hidden=!$("lab-show-preview").checked||!$("lab-video").srcObject;
        if(!$("lab-show-preview").checked){$("lab-network-preview").hidden=true;$("lab-network-preview").width=$("lab-network-preview").width;}
    });
    $("lab-page-preset")?.addEventListener("change",()=>{if($("lab-page-preset").value)$("lab-url").value=$("lab-page-preset").value;});
    $("lab-once").addEventListener("click",()=>void start(true));$("lab-stop").addEventListener("click",()=>stop());
    $("lab-config").addEventListener("change",()=>{stop();workspace.clear();lastInput=null;$("lab-transport-info").textContent="";if($("lab-body-preview"))$("lab-body-preview").textContent="";$("lab-source-info").textContent="No source active.";sourceFields();status("waiting","Settings changed · Start or Sample once to apply");buttons();});
    $("pause-plots").addEventListener("click",()=>{paused=!paused;workspace.setFrozen(paused);lastAccepted=-Infinity;buttons();status("ready",paused?"Receiving · plots paused":"Source active");});
    document.addEventListener("visibilitychange",()=>{if(document.hidden&&running)stop("disconnected","Hidden tab · source stopped");});
    function download(blob,name){
        const url=URL.createObjectURL(blob),anchor=document.createElement("a");anchor.href=url;anchor.download=name;anchor.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
    }
    $("lab-download").addEventListener("click",()=>{const sample=workspace.sample;if(sample?.sourceId===generation)download(new Blob([sample.buffer.slice(0)],{type:"application/octet-stream"}),`entropy-lab-${options.kind}-${options.transform}.bin`);});
    function renderRecords(){
        const body=$("lab-measurements").tBodies[0];body.replaceChildren();
        const labels=["Source","Representation","Window bytes","Shannon bits/B","Session sampled MB/s"];
        for(const record of records){
            const row=document.createElement("tr");row.setAttribute("role","row");
            const values=[record.source,record.representation,record.window.bytes.toLocaleString(),record.statistics.entropy.toFixed(5),record.meter.bytesPerSecond===null?"—":(record.meter.bytesPerSecond/1e6).toFixed(4)];
            values.forEach((value,index)=>{
                const cell=document.createElement("td"),content=document.createElement("span");cell.setAttribute("role","cell");cell.dataset.label=labels[index];content.textContent=value;cell.append(content);
                if(index===0){
                    const note=document.createElement("small");note.className="lab-record-note";
                    note.textContent=`${record.frozen?"Frozen":"Live"} sample #${record.window.frame} · ${record.capturedAt?new Date(record.capturedAt).toLocaleTimeString():"capture time unavailable"}`;
                    content.append(note);cell.title=`Captured: ${record.capturedAt||"unavailable (older record)"}\nRecorded: ${record.recordedAt||record.timestamp}\nMeters describe the source session at recording time.`;
                }
                row.append(cell);
            });body.append(row);
        }
        try{sessionStorage.setItem(storageKey,JSON.stringify(records));}catch(_){}
        buttons();
    }
    $("lab-record").addEventListener("click",()=>{
        const sample=workspace.sample;if(sample?.sourceId!==generation)return;
        const recordedAt=new Date().toISOString(),now=performance.now();
        records.push({timestamp:recordedAt,recordedAt,capturedAt:new Date(sample.time).toISOString(),sampleAgeMs:Math.max(0,now-sample.captureTime),source:sample.sourceName,encoding:sample.encoding,representation:sample.representation,
            sourceKind:options.selection,transportKind:options.kind,registeredSource:options.registeredId||null,upstreamKind:options.registeredKind||null,settings:{sampleBytes:options.size,cadenceMs:options.refresh,cadenceApplied:options.cadenceApplied},window:{offset:sample.offset,bytes:sample.analyzedBytes,frame:sample.frame},
            statistics:{entropy:sample.entropy,mean:sample.mean,stddev:sample.stddev,ones:sample.ones,zeros:sample.zeros,bitOnes:Array.from(sample.bitOnes)},
            meter:{...meter.snapshot(now),scope:"source-session",measuredAt:recordedAt},frozen:workspace.frozen});
        if(records.length>50)records.shift();renderRecords();
    });
    $("lab-clear-records").addEventListener("click",()=>{records.length=0;renderRecords();});
    $("lab-export").addEventListener("click",()=>download(new Blob([JSON.stringify({format:"entropy-lab-measurements-v1",rateUnit:"sampled bytes/second",records},null,2)],{type:"application/json"}),"entropy-lab-measurements.json"));
    if($("lab-registered"))void(async()=>{
        try{
            const response=await fetch("/lab/sources",{cache:"no-store"});if(!response.ok)throw new Error("Source catalog unavailable");catalog=await response.json();
            if(!Array.isArray(catalog.sources))throw new Error("Invalid source catalog");
            $("lab-registered").replaceChildren();
            const available=catalog.sources.filter(source=>document.body.dataset.lab==="sources"?source.width>0:!source.width);
            for(const source of available){const option=document.createElement("option");option.value=source.id;option.textContent=`${source.name} (${source.kind})`;$("lab-registered").append(option);}
            if(!available.length){const option=document.createElement("option");option.value="";option.textContent="No sources registered";$("lab-registered").append(option);}
            $("lab-catalog-info").textContent=catalog.sources.length?`${catalog.sources.length} registered source(s) · bridge ${catalog.allowed?"available":"restricted to loopback clients"} · operator setup: -lab-sources=FILE`:document.body.dataset.lab==="sources"?"No network-camera registrations are active. Local browser sources work without a registry. Install the prepared JSON and restart with -lab-sources=lab-sources.json to add network cameras.":"No registered sources are active. Preparing JSON here does not load it: save it on the server and restart with -lab-sources=lab-sources.json. Browser HTTP still needs target-site CORS permission.";
            sourceFields();
        }catch(error){$("lab-catalog-info").textContent=error.message;}
    })();
    window.addEventListener("pagehide",()=>{stopTransport();generation++;workspace.destroy();});
    sourceFields();renderRecords();buttons();
})();
