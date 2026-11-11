"use strict";

// Producers publish bounded binary samples; Stop/abort owns every timer, socket,
// reader and media track. Experimental inputs are never sent to DRBG seeding.
(() => {
    const helpers=window.EntropyLabSamples;
    const aborted=()=>new DOMException("Source stopped","AbortError");
    function guard(signal,close) {if(signal.aborted){close();throw aborted();} signal.addEventListener("abort",close,{once:true});}
    function periodic(signal,refresh,produce,onError) {
        let timer;
        const close=()=>clearTimeout(timer);
        guard(signal,close);
        const tick=async()=>{
            const started=performance.now();
            try {await produce();} catch(error) {if(!signal.aborted)onError(error);return;}
            if(!signal.aborted)timer=setTimeout(tick,Math.max(0,refresh-(performance.now()-started)));
        };
        void tick();
        return close;
    }
    async function httpPrefix(url,count,signal,info) {
        const timeout=new AbortController();
        const stop=()=>timeout.abort();
        signal.addEventListener("abort",stop,{once:true});
        const timer=setTimeout(stop,10000);
        let reader;
        try {
            if(signal.aborted)throw aborted();
            const response=await fetch(url,{signal:timeout.signal,credentials:"omit",redirect:"follow",cache:"no-store"});
            info(window.EntropyLabRegistration.httpStatus(response.status,response.redirected));
            if(!response.ok)throw new Error(`HTTP source returned ${response.status}`);
            if(!response.body)throw new Error("HTTP source has no readable response body");
            reader=response.body.getReader();
            const output=new Uint8Array(count);let used=0;
            while(used<count){
                const {done,value}=await reader.read();
                if(done)break;
                const take=Math.min(value.length,count-used);output.set(value.subarray(0,take),used);used+=take;
            }
            if(!used)throw new Error("HTTP source returned no bytes");
            return {bytes:output.subarray(0,used),encoding:`HTTP ${response.status} body prefix · ${response.headers.get("Content-Type")||"unspecified type"}`};
        } catch(failure) {
            if(timeout.signal.aborted&&!signal.aborted)throw new Error("HTTP source timed out");
            if(failure instanceof TypeError)throw new Error("Browser HTTP fetch failed: the target may block CORS or redirects, or be unreachable. Choose a registered server source for ordinary web pages.");
            throw failure;
        } finally {clearTimeout(timer);signal.removeEventListener("abort",stop);if(reader){await reader.cancel().catch(()=>{});reader.releaseLock();}}
    }
    async function audioInputs() {
        if(!navigator.mediaDevices?.enumerateDevices)return null;
        const devices=await navigator.mediaDevices.enumerateDevices();
        return devices.filter(device=>device.kind==="audioinput"&&device.deviceId&&device.deviceId!=="default")
            .map(device=>({id:device.deviceId,label:device.label}));
    }
    async function start(options,{signal,emit,ready,end,error,video,previewEnabled=()=>false,previewCanvas,info=()=>{}}) {
        const {kind,size,refresh}=options;
        if(["crypto","counter","constant","pattern"].includes(kind)) {
            let sequence=0;ready();
            return periodic(signal,refresh,()=>{
                const bytes=helpers.generate(kind,size,{value:options.value,bytes:options.pattern,sequence});
                sequence=(sequence+size)%(kind==="pattern"?options.pattern.length:256);
                emit(bytes,kind==="crypto"?"Browser Web Crypto bytes":`Deterministic ${kind} bytes`);
            },error);
        }
        if(kind==="file") {
            if(!options.file?.size||options.file.size>32*1024*1024)throw new Error("Choose a nonempty local file up to 32 MiB");
            let offset=0;ready();
            return periodic(signal,refresh,async()=>{
                const start=offset;
                const bytes=new Uint8Array(await options.file.slice(start,Math.min(options.file.size,start+size)).arrayBuffer());
                if(signal.aborted)return;
                offset+=bytes.length;emit(bytes,`Local file bytes [${start}, ${offset}) · ${options.file.name}`);
                if(offset>=options.file.size)end("File reached EOF");
            },error);
        }
        if(kind==="http") {
            const url=helpers.endpoint(options.url,"http");ready();
            return periodic(signal,refresh,async()=>{const sample=await httpPrefix(url,size,signal,info);if(!signal.aborted)emit(sample.bytes,sample.encoding);},error);
        }
        if(kind==="websocket") {
            const url=helpers.endpoint(options.url,"websocket");
            const socket=new WebSocket(url);socket.binaryType="arraybuffer";
            let sourceReady=false;
            const timer=setTimeout(()=>{socket.close();if(!signal.aborted)error(new Error("WebSocket connection timed out"));},10000);
            const close=()=>{clearTimeout(timer);socket.close();if(options.camera&&previewCanvas){previewCanvas.hidden=true;previewCanvas.width=previewCanvas.width;}};guard(signal,close);
            socket.onopen=()=>{clearTimeout(timer);if(!signal.aborted){if(options.camera){socket.send(JSON.stringify(options.credentials));options.credentials=null;}if(!options.events)ready();}};
            socket.onmessage=event=>{
                if(signal.aborted)return;
                if(options.events&&typeof event.data==="string"){
                    try{
                        if(event.data.length>1024)throw new Error("Bridge metadata exceeds limit");
                        const value=JSON.parse(event.data),kind=options.registeredKind;
                        if(value.type==="source-ready"&&value.kind===kind&&!sourceReady){
                            sourceReady=true;ready();
                            info(kind==="udp"?"UDP listener bound on entropy-server · send nonempty datagrams to its registered local address.":kind==="tcp"?"TCP producer connected · waiting for available bytes; no request or handshake is sent.":options.camera?"Camera decoder started · waiting for RGB bytes.":"Registered source opened · waiting for data.");
                        }else if(value.type==="http-status"&&sourceReady&&Number.isInteger(value.status)&&value.status>=100&&value.status<=599&&Number.isInteger(value.redirects)&&value.redirects>=0&&value.redirects<=2){
                            info(window.EntropyLabRegistration.httpStatus(value.status,value.redirects));
                        }else throw new Error("Unexpected bridge metadata");
                    }catch(failure){close();error(failure);}
                    return;
                }
                if(!(event.data instanceof ArrayBuffer)||!event.data.byteLength||event.data.byteLength>Math.min(helpers.MAX_BYTES,options.maxBytes||helpers.MAX_BYTES)){
                    close();error(new Error("Source must send nonempty binary messages within the configured lab byte limit (at most 8 MiB)"));return;
                }
                const bytes=new Uint8Array(event.data);
                if(options.camera){
                    const {width,height}=options.camera;
                    if(options.preview&&previewEnabled()&&bytes.length===width*height*3){
                        if(previewCanvas.width!==width)previewCanvas.width=width;if(previewCanvas.height!==height)previewCanvas.height=height;
                        const rgba=new Uint8ClampedArray(width*height*4);
                        for(let i=0,j=0;i<bytes.length;i+=3,j+=4){rgba[j]=bytes[i];rgba[j+1]=bytes[i+1];rgba[j+2]=bytes[i+2];rgba[j+3]=255;}
                        previewCanvas.getContext("2d").putImageData(new ImageData(rgba,width,height),0,0);previewCanvas.hidden=false;
                    }
                    emit(bytes.subarray(0,size),`Decoded RGB8 ${width}×${height} row-major prefix · network camera`);
                }else emit(bytes,options.registeredKind==="webpage"?"Registered HTTP body prefix":"Binary WebSocket message");
            };
            socket.onerror=()=>{if(!signal.aborted)error(new Error("WebSocket failed: check address, Origin policy, TLS and bridge availability"));};
            socket.onclose=event=>{
                clearTimeout(timer);
                if(signal.aborted)return;
                if(event.code===1000)end(event.reason||"WebSocket source closed");
                else error(new Error(event.reason||`WebSocket closed (${event.code})`));
            };
            return close;
        }
        if(kind==="camera") {
            if(!navigator.mediaDevices?.getUserMedia)throw new Error("Camera access needs a supported browser and HTTPS or localhost");
            const [width,height]=options.resolution.split("x").map(Number);
            const stream=await navigator.mediaDevices.getUserMedia({video:{width:{ideal:width},height:{ideal:height}},audio:false});
            let cancelTimer=()=>{};
            const close=()=>{cancelTimer();stream.getTracks().forEach(track=>track.stop());if(video.srcObject===stream){video.pause();video.srcObject=null;video.hidden=true;}};
            guard(signal,close);
            try {
                video.srcObject=stream;video.hidden=!previewEnabled();await video.play();
                if(signal.aborted)throw aborted();
                const canvas=document.createElement("canvas");canvas.width=width;canvas.height=height;
                const context=canvas.getContext("2d",{willReadFrequently:true});
                const channels=options.format==="rgba"?4:3;
                const settings=stream.getVideoTracks()[0].getSettings();ready();
                cancelTimer=periodic(signal,refresh,()=>{
                    if(video.readyState<2)return;
                    context.drawImage(video,0,0,width,height);
                    const rgba=context.getImageData(0,0,width,height).data;
                    const bytes=new Uint8Array(Math.min(size,width*height*channels));
                    if(channels===4)bytes.set(rgba.subarray(0,bytes.length));
                    else for(let i=0;i<bytes.length;i++)bytes[i]=rgba[Math.floor(i/3)*4+i%3];
                    emit(bytes,`${channels===4?"RGBA8":"RGB8"} ${width}×${height} row-major prefix · camera ${settings.width||"?"}×${settings.height||"?"} @ ${settings.frameRate||"?"} fps`);
                },error);
                return close;
            }catch(failure){close();throw failure;}
        }
        if(kind==="microphone") {
            const Audio=window.AudioContext||window.webkitAudioContext;
            if(!navigator.mediaDevices?.getUserMedia||!Audio)throw new Error("Audio input needs a supported browser, Web Audio and HTTPS or localhost");
            if(signal.aborted)throw aborted();
            const context=new Audio();
            let stream,closed=false,cancelTimer=()=>{};
            const close=()=>{
                cancelTimer();stream?.getTracks().forEach(track=>track.stop());
                if(!closed){closed=true;void context.close().catch(()=>{});}
            };
            guard(signal,close);
            try{
                await context.resume();
                if(signal.aborted)throw aborted();
                const audio={echoCancellation:false,noiseSuppression:false,autoGainControl:false};
                if(options.audioDeviceId)audio.deviceId={exact:options.audioDeviceId};
                stream=await navigator.mediaDevices.getUserMedia({audio,video:false});
                if(signal.aborted)throw aborted();
                const track=stream.getAudioTracks()[0];
                if(!track||track.readyState==="ended")throw new Error("Audio input disconnected; choose an available input and Start again");
                track.addEventListener("ended",()=>{
                    if(!closed&&!signal.aborted){close();error(new Error("Audio input disconnected; choose an available input and Start again"));}
                },{once:true});
                const source=context.createMediaStreamSource(stream),analyser=context.createAnalyser(),mute=context.createGain();
                analyser.fftSize=Math.max(32,2**Math.floor(Math.log2(Math.min(32768,Math.max(32,size/2)))));
                mute.gain.value=0;source.connect(analyser);analyser.connect(mute);mute.connect(context.destination);
                info(`Audio input: ${track.label||"system default"}. Choose a different input or use your system sound settings to select the physical line-in port.`);
                const samples=new Float32Array(analyser.fftSize);ready();
                cancelTimer=periodic(signal,refresh,()=>{
                    analyser.getFloatTimeDomainData(samples);
                    const length=Math.min(size,samples.length*2),bytes=new Uint8Array(Math.ceil(length/2)*2),view=new DataView(bytes.buffer);
                    for(let i=0;i<bytes.length/2;i++){
                        const sample=Math.max(-1,Math.min(1,samples[i]));
                        view.setInt16(i*2,Math.round(sample*(sample<0?32768:32767)),true);
                    }
                    emit(bytes.subarray(0,length),`PCM16LE mono · ${context.sampleRate} samples/s · ${track.label||"system default audio input"} · latest audio window (may overlap)`);
                },error);
                return close;
            }catch(failure){
                close();
                if(failure.name==="NotAllowedError")throw new Error("Audio permission denied; allow audio input for this site in your browser, then Start again");
                if(["NotFoundError","OverconstrainedError"].includes(failure.name))throw new Error("Selected audio input is unavailable; refresh inputs and choose another device or System default");
                if(failure.name==="NotReadableError")throw new Error("Audio input could not be opened; check whether it is disconnected or in use by another application");
                throw failure;
            }
        }
        if(kind==="serial") {
            if(!navigator.serial?.requestPort)throw new Error("Web Serial is unavailable in this browser; use a registered socket adapter");
            const port=await navigator.serial.requestPort();
            if(signal.aborted)throw aborted();
            await port.open({baudRate:options.baud});
            let reader;
            const close=()=>{void reader?.cancel().catch(()=>{});};
            guard(signal,()=>{close();if(!reader)void port.close().catch(()=>{});});
            reader=port.readable.getReader();ready();
            void(async()=>{
                try{
                    while(!signal.aborted){const {done,value}=await reader.read();if(done)break;if(value?.length)emit(value.slice(0,size),`Serial read prefix · ${options.baud} baud`);}
                    if(!signal.aborted)end("Serial source closed");
                }catch(failure){if(!signal.aborted)error(failure);}
                finally{reader.releaseLock();await port.close().catch(()=>{});}
            })();
            return close;
        }
        throw new Error("Unknown source type");
    }
    window.EntropyLabAdapters=Object.freeze({start,audioInputs});
})();
