# Completes every case in cases.txt with TabExpansion2, the function PowerShell's TAB calls, which
# runs the native completer the generated script registers. PowerShell shows no messages; a
# candidate's tooltip is its description, or its own text when it has none.
# Prints "== <case>", then "<value><TAB><description>" per candidate.
# Usage: pwsh -NoProfile -File pwsh.ps1 <completion script> <cases file>
param($Script, $Cases)
$ErrorActionPreference = 'Stop'
. (Join-Path $PWD $Script)
foreach ($entry in Get-Content $Cases) {
    if (-not $entry -or $entry.StartsWith('#')) { continue }
    $name, $line = ($entry -split "`t")[0, 1]
    $line = $line.TrimEnd('|')
    "== $name"
    try {
        $matches = (TabExpansion2 -inputScript $line -cursorColumn $line.Length).CompletionMatches
    } catch {
        # The script answers an empty string to stop PowerShell's fallback to file names; the
        # completion system throws on it, and PSReadLine's TAB then offers nothing.
        if ("$_" -notlike '*"completionText" is null*') { throw }
        '<nothing: the file fallback stopped>'
        continue
    }
    foreach ($m in $matches) {
        $desc = if ($m.ToolTip -ne $m.CompletionText) { $m.ToolTip } else { '' }
        "$($m.CompletionText)`t$desc"
    }
}
