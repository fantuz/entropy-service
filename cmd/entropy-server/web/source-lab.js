"use strict";

(() => {
    const $=id=>document.getElementById(id), helpers=window.EntropyLabSamples;
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
            $("lab-rate").textContent=data.bytesPerSecond===null?"—":(data.bytesPerSecond/1e6).toFixed(4);
            $("lab-interval").textContent=data.meanIntervalMs===null?"—":data.meanIntervalMs.toFixed(2);
            $("lab-analysis-bytes").textContent=analysedBytes.toLocaleString();$("lab-analysis-frames").textContent=analysedFrames.toLocaleString();
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
    function stopTransport(){controller?.abort();controller=null;const close=cleanup;cleanup=null;if(close)close();meter.stop(performance.now());running=false;updateMeters();}
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
            const base=selected.startsWith("self")?new URL(location.href):helpers.endpoint($("lab-url").value,"http");
            const websocket=selected.endsWith("ws");
            const url=new URL(websocket?"/stream":"/v1/data/random",base);
            url.searchParams.set("bytes",size);url.searchParams.set("refresh",refresh);
            if(websocket)url.protocol=url.protocol==="https:"?"wss:":"ws:";
            result.kind=websocket?"websocket":"http";result.url=url.href;result.label+=` · ${base.host}`;
        }else if(selected==="bridge"){
            const source=catalog.sources.find(value=>value.id===$("lab-registered").value);
            if(!source||!catalog.allowed)throw new Error("No permitted registered source: check -lab-sources and loopback/-lab-allow-remote access");
            if(size>Math.min(source.max_bytes,catalog.max_bytes))throw new Error("Sample exceeds the registered source byte limit");
            const url=new URL("/lab/stream",location.href);url.protocol=location.protocol==="https:"?"wss:":"ws:";
            url.searchParams.set("source",source.id);url.searchParams.set("bytes",size);url.searchParams.set("refresh",refresh);
            result.kind="websocket";result.registeredId=source.id;result.registeredKind=source.kind;result.url=url.href;result.label=`Registered ${source.name} (${source.kind})`;
        }else if(selected==="http"||selected==="websocket"){
            const url=helpers.endpoint($("lab-url").value,selected);result.url=url.href;result.label+=` · ${url.host}`;
        }
        return result;
    }
    async function start(single=false){
        let requested;try{requested=readOptions();}catch(error){status("error",error.message);return;}
        if(!requested)return;
        stopTransport();generation++;options=requested;once=single;finishing=false;paused=false;running=true;
        lastInput=null;previous=null;frame=0;analysedBytes=0;analysedFrames=0;lastAccepted=-Infinity;lastSubmittedFrame=0;
        meter.reset(performance.now());workspace.clear();workspace.setFrozen(false);controller=new AbortController();
        const signal=controller.signal,token=generation;
        status("waiting","Connecting or awaiting source permission…");buttons();updateMeters();
        try{
            const close=await window.EntropyLabAdapters.start(options,{signal,video:$("lab-video"),
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
        document.querySelectorAll("[data-modes]").forEach(label=>{
            label.hidden=!label.dataset.modes.split(" ").includes(kind);
            label.querySelectorAll("input,select").forEach(control=>{control.disabled=label.hidden;});
        });
        if($("lab-url"))$("lab-url").required=["peer-ws","peer-http","http","websocket"].includes(kind);
        if($("lab-file"))$("lab-file").required=kind==="file";
        const source=catalog.sources.find(value=>value.id===$("lab-registered")?.value);
        const limit=kind==="bridge"?Math.min(catalog.max_bytes,source?.max_bytes||helpers.MAX_BYTES):helpers.MAX_BYTES;
        $("lab-bytes").max=limit;if(Number($("lab-bytes").value)>limit)$("lab-bytes").value=limit;
    }
    $("lab-config").addEventListener("submit",event=>{event.preventDefault();void start(false);});
    $("lab-once").addEventListener("click",()=>void start(true));$("lab-stop").addEventListener("click",()=>stop());
    $("lab-config").addEventListener("change",()=>{stop();workspace.clear();lastInput=null;$("lab-source-info").textContent="No source active.";sourceFields();status("waiting","Settings changed · Start or Sample once to apply");buttons();});
    $("pause-plots").addEventListener("click",()=>{paused=!paused;workspace.setFrozen(paused);lastAccepted=-Infinity;buttons();status("ready",paused?"Receiving · plots paused":"Source active");});
    document.addEventListener("visibilitychange",()=>{if(document.hidden&&running)stop("disconnected","Hidden tab · source stopped");});
    function download(blob,name){
        const url=URL.createObjectURL(blob),anchor=document.createElement("a");anchor.href=url;anchor.download=name;anchor.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
    }
    $("lab-download").addEventListener("click",()=>{const sample=workspace.sample;if(sample?.sourceId===generation)download(new Blob([sample.buffer.slice(0)],{type:"application/octet-stream"}),`entropy-lab-${options.kind}-${options.transform}.bin`);});
    function renderRecords(){
        const body=$("lab-measurements").tBodies[0];body.replaceChildren();
        for(const record of records){const row=document.createElement("tr");for(const value of [record.source,record.representation,record.window.bytes,record.statistics.entropy.toFixed(5),record.meter.bytesPerSecond===null?"—":(record.meter.bytesPerSecond/1e6).toFixed(4)]){const cell=document.createElement("td");cell.textContent=value;row.append(cell);}body.append(row);}
        try{sessionStorage.setItem(storageKey,JSON.stringify(records));}catch(_){}
        buttons();
    }
    $("lab-record").addEventListener("click",()=>{
        const sample=workspace.sample;if(sample?.sourceId!==generation)return;
        records.push({timestamp:new Date().toISOString(),source:sample.sourceName,encoding:sample.encoding,representation:sample.representation,
            sourceKind:options.selection,transportKind:options.kind,registeredSource:options.registeredId||null,upstreamKind:options.registeredKind||null,settings:{sampleBytes:options.size,cadenceMs:options.refresh,cadenceApplied:options.cadenceApplied},window:{offset:sample.offset,bytes:sample.analyzedBytes,frame:sample.frame},
            statistics:{entropy:sample.entropy,mean:sample.mean,stddev:sample.stddev,ones:sample.ones,zeros:sample.zeros,bitOnes:Array.from(sample.bitOnes)},
            meter:meter.snapshot(performance.now()),frozen:workspace.frozen});
        if(records.length>50)records.shift();renderRecords();
    });
    $("lab-clear-records").addEventListener("click",()=>{records.length=0;renderRecords();});
    $("lab-export").addEventListener("click",()=>download(new Blob([JSON.stringify({format:"entropy-lab-measurements-v1",rateUnit:"sampled bytes/second",records},null,2)],{type:"application/json"}),"entropy-lab-measurements.json"));
    if($("lab-registered"))void(async()=>{
        try{
            const response=await fetch("/lab/sources",{cache:"no-store"});if(!response.ok)throw new Error("Source catalog unavailable");catalog=await response.json();
            if(!Array.isArray(catalog.sources))throw new Error("Invalid source catalog");
            $("lab-registered").replaceChildren();
            for(const source of catalog.sources){const option=document.createElement("option");option.value=source.id;option.textContent=`${source.name} (${source.kind})`;$("lab-registered").append(option);}
            if(!catalog.sources.length){const option=document.createElement("option");option.value="";option.textContent="No sources registered";$("lab-registered").append(option);}
            $("lab-catalog-info").textContent=`${catalog.sources.length} registered source(s) · bridge ${catalog.allowed?"available":"restricted to loopback clients"} · operator setup: -lab-sources=FILE`;
            sourceFields();
        }catch(error){$("lab-catalog-info").textContent=error.message;}
    })();
    window.addEventListener("pagehide",()=>{stopTransport();generation++;workspace.destroy();});
    sourceFields();renderRecords();buttons();
})();
