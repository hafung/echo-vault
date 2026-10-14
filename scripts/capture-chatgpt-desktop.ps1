# Invoked by Echo Vault, with a JSON job list on stdin. Only compact JSON records
# are written to stdout. This helper does not start, restart or navigate Desktop.
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = New-Object System.Text.UTF8Encoding($false)
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$script:socket = $null
$script:sequence = 0

function Write-Record($value) {
    [Console]::Out.WriteLine(($value | ConvertTo-Json -Depth 100 -Compress))
    [Console]::Out.Flush()
}

function Read-ModuleName([string]$executable) {
    $archive = Join-Path (Split-Path -Parent $executable) 'resources\app.asar'
    $stream = [IO.File]::Open($archive, 'Open', 'Read', 'ReadWrite')
    try {
        $reader = New-Object IO.BinaryReader($stream)
        $header = $reader.ReadBytes(16)
        if ($header.Length -ne 16) { throw 'Invalid archive header' }
        $length = [BitConverter]::ToUInt32($header, 12)
        if ($length -gt 16777216) { throw 'Archive header too large' }
        $bytes = $reader.ReadBytes($length)
        if ($bytes.Length -ne $length) { throw 'Truncated archive header' }
        $index = [Text.Encoding]::UTF8.GetString($bytes) | ConvertFrom-Json
        $names = @($index.files.webview.files.assets.files.PSObject.Properties.Name | Where-Object { $_ -match '^chatgpt-request-[a-zA-Z0-9]+\.js$' })
        if ($names.Count -ne 1) { throw 'Unsupported Desktop request module' }
        return '/assets/' + $names[0]
    } finally { $stream.Dispose() }
}

function Open-Cdp([string]$address, [int]$port) {
    $uri = [Uri]$address
    if ($uri.Scheme -ne 'ws' -or $uri.Host -notin @('127.0.0.1','localhost') -or $uri.Port -ne $port) { throw 'Non-local inspector address' }
    $script:socket = New-Object Net.WebSockets.ClientWebSocket
    $script:socket.Options.Proxy = $null
    $cancel = New-Object Threading.CancellationTokenSource
    $cancel.CancelAfter(5000)
    try { [void]$script:socket.ConnectAsync($uri,$cancel.Token).GetAwaiter().GetResult() } finally { $cancel.Dispose() }
}

function Invoke-Cdp([string]$expression) {
    $script:sequence++
    $request = @{ id=$script:sequence; method='Runtime.evaluate'; params=@{ expression=$expression; returnByValue=$true; awaitPromise=$true } } | ConvertTo-Json -Depth 10 -Compress
    $bytes = [Text.Encoding]::UTF8.GetBytes($request)
    $cancel = New-Object Threading.CancellationTokenSource
    $cancel.CancelAfter(95000)
    try {
        [void]$script:socket.SendAsync([ArraySegment[byte]]::new($bytes),[Net.WebSockets.WebSocketMessageType]::Text,$true,$cancel.Token).GetAwaiter().GetResult()
        while ($true) {
            $output = New-Object IO.MemoryStream
            try {
                $buffer = New-Object byte[] 16384
                do {
                    $frame = $script:socket.ReceiveAsync([ArraySegment[byte]]::new($buffer),$cancel.Token).GetAwaiter().GetResult()
                    if ($frame.MessageType -eq [Net.WebSockets.WebSocketMessageType]::Close) { throw 'Inspector closed' }
                    $output.Write($buffer,0,$frame.Count)
                    if ($output.Length -gt 67108864) { throw 'Inspector response too large' }
                } while (!$frame.EndOfMessage)
                $response = [Text.Encoding]::UTF8.GetString($output.ToArray()) | ConvertFrom-Json
            } finally { $output.Dispose() }
            if ($response.id -ne $script:sequence) { continue }
            if ($response.error -or $response.result.exceptionDetails) { throw 'Page evaluation failed' }
            if (!$response.result.result.value) { throw 'Page returned no result' }
            return [string]$response.result.result.value
        }
    } finally { $cancel.Dispose() }
}

try {
    $inputValue = [Console]::In.ReadToEnd() | ConvertFrom-Json
    $port = [int]$inputValue.port
    if ($port -ne 19222) { throw 'Unexpected inspector port' }
    $listeners = @(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue)
    if (!$listeners.Count) { Write-Record @{kind='unavailable'}; exit 0 }
    if (@($listeners | Where-Object LocalAddress -notin @('127.0.0.1','::1')).Count) { throw 'Inspector must listen only on loopback' }
    $owner = Get-CimInstance Win32_Process | Where-Object { $_.ProcessId -eq $listeners[0].OwningProcess }
    if ($owner.Name -ne 'ChatGPT.exe' -or $owner.ExecutablePath -notmatch '[\\/]OpenAI\.[^\\/]+[\\/]app[\\/]ChatGPT\.exe$') { throw 'Inspector is not the installed ChatGPT Desktop' }
    $module = Read-ModuleName $owner.ExecutablePath
    $targets = @(Invoke-RestMethod -Uri "http://127.0.0.1:$port/json/list" -TimeoutSec 3 | Where-Object { $_.type -eq 'page' -and $_.url -match '^app://-/' -and $_.webSocketDebuggerUrl })
    if (!$targets.Count) { Write-Record @{kind='unavailable'}; exit 0 }
    Open-Cdp $targets[0].webSocketDebuggerUrl $port
    $deadline = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds() + 90000
    $template = @'
(async()=>{
  const job=__JOB__, deadline=__DEADLINE__;
  let conversation=null, cursor=null, pages=0;
  const messages=new Map();
  const hasContent=m=>(m.content?.parts||[]).some(p=>typeof p==='string'&&p.trim()||typeof p?.text==='string'&&p.text.trim())||Boolean(m.content?.text?.trim())||Boolean(m.content?.thoughts?.length)||Boolean(m.metadata?.model_dil_v2)||Boolean(m.metadata?.genui_components?.length);
  const merge=m=>{const previous=messages.get(m.id);if(!previous||!hasContent(previous)&&hasContent(m))messages.set(m.id,m)};
  const done=()=>JSON.stringify({kind:'conversation',id:job.id,complete:!cursor,cursor:cursor||'',pages,
    conversation:{...conversation,messages:[...messages.values()],
      page_info:{has_previous_page:!!cursor,has_next_page:false}}});
  try {
    if(location.origin!=='app://-')throw Error('Unexpected page');
    const {Request}=await import(__MODULE__);
    const get=(path,query)=>{
      const remaining=deadline-Date.now();
      if(remaining<1000)throw Error('Budget exhausted');
      return Request.safeGet(path,{parameters:{path:{conversation_id:job.id},query},signal:AbortSignal.timeout(Math.min(15000,remaining))});
    };
    conversation=await get('/conversations/{conversation_id}',{num_turns:10,include_has_versions:true});
    if((conversation.conversation_id||conversation.id)!==job.id||!Array.isArray(conversation.messages)||!conversation.page_info)throw Error('Invalid conversation');
    for(const message of conversation.messages)merge(message);
    // Keep the current node from the newest page even while older pages are added.
    conversation.current_node=conversation.current_node||conversation.page_info.end_cursor||conversation.messages.at(-1)?.id;
    cursor=job.cursor||(conversation.page_info.has_previous_page?conversation.page_info.start_cursor:null);
    if(conversation.page_info.has_previous_page&&!cursor)throw Error('Missing cursor');
    pages=1;
    const seen=new Set();
    while(cursor&&pages<12&&deadline-Date.now()>2000){
      if(seen.has(cursor))throw Error('Cursor did not advance');
      seen.add(cursor);
      const page=await get('/conversations/{conversation_id}/messages',{before:cursor,num_turns:10,include_has_versions:true});
      if(!Array.isArray(page.messages)||!page.page_info||page.conversation_id&&page.conversation_id!==job.id)throw Error('Invalid page');
      for(const message of page.messages)merge(message);
      const next=page.page_info.has_previous_page?page.page_info.start_cursor:null;
      if(page.page_info.has_previous_page&&!next)throw Error('Missing cursor');
      cursor=next;pages++;
    }
    return done();
  }catch(error){
    // Partial progress is useful, but an unchanged/invalid cursor must not become
    // a completed checkpoint. No server error body or credentials leave the page.
    if(pages&&cursor)return done();
    return JSON.stringify({kind:'error',id:job.id,status:error.responseStatus||error.status||0});
  }
})()
'@
    foreach ($job in $inputValue.jobs) {
        if ([DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds() -ge $deadline - 2000) { break }
        if ($job.id -notmatch '^[a-zA-Z0-9-]{1,128}$') { throw 'Invalid conversation ID' }
        $expression = $template.Replace('__JOB__',($job | ConvertTo-Json -Compress)).Replace('__MODULE__',($module | ConvertTo-Json -Compress)).Replace('__DEADLINE__',[string]$deadline)
        try {
            $value = Invoke-Cdp $expression
            # Send the page's already encoded JSON directly; ConvertTo-Json would
            # otherwise truncate deeply nested message metadata in PowerShell 5.
            [Console]::Out.WriteLine($value)
            [Console]::Out.Flush()
        } catch { Write-Record @{kind='error';id=$job.id;status=0} }
    }
} catch { Write-Record @{kind='unavailable'} }
finally { if ($script:socket) { $script:socket.Dispose() } }
