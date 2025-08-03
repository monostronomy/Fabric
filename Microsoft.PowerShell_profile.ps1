# Define the base directories with Fabric subdirectory
$obsidian_base = "C:\Users\jeffd\OneDrive\Documents\Fabric"
$OBS = "C:\Users\jeffd\OneDrive\Documents\Fabric"
$fabricPath = "G:\GitHub\fabric\fabric.exe"

# Get pattern files with error handling
$pattern_dir = "$env:USERPROFILE\.config\fabric\patterns"
if (-not (Test-Path $pattern_dir)) {
    Write-Error "Pattern directory not found: $pattern_dir"
    return
}

$pattern_files = Get-ChildItem -Path "$pattern_dir\*" -ErrorAction Stop

# Create functions for each pattern
foreach ($pattern_file in $pattern_files) {
    $pattern_name = $pattern_file.BaseName
    
    # Remove function if it already exists
    if (Get-Command -Name $pattern_name -ErrorAction SilentlyContinue) {
        Remove-Item -Path "Function:\$pattern_name" -ErrorAction SilentlyContinue
    }

    $functionScript = @"
    function global:$pattern_name {
        param (
            [Parameter(ValueFromRemainingArguments=`$true)]
            [string[]]`$promptWords
        )
        
        # Join all words into a single prompt
        `$prompt = `$promptWords -join ' '
        
        `$date_stamp = Get-Date -Format "yyyy-MM-dd"
        
        # Create a truncated, sanitized version of the prompt for the filename
        `$safe_title = (`$prompt -replace '[\\/:*?"<>|]', '-') -replace '\s+', '-'
        # Take first 50 characters and trim any trailing hyphens
        `$truncated_title = (`$safe_title.Substring(0, [Math]::Min(50, `$safe_title.Length))).TrimEnd('-')
        `$output_path = "$obsidian_base\`$date_stamp-$pattern_name-`$truncated_title.md"
        
        try {
            # Updated command syntax
            & '$fabricPath' --pattern $pattern_name "`$prompt" -o "`$output_path"
            Start-Sleep -Seconds 1  # Give the file system a moment
            
            if (Test-Path `$output_path) {
                if ((Get-Item `$output_path).Length -gt 0) {
                    Write-Host "Successfully created entry: `$output_path"
                } else {
                    Write-Warning "File was created but is empty: `$output_path"
                }
            } else {
                Write-Error "Failed to create file: `$output_path"
            }
        }
        catch {
            Write-Error "Error running fabric command: `$_"
        }
    }
"@

    try {
        Invoke-Expression $functionScript
        Write-Host "Created function for pattern: $pattern_name"
    }
    catch {
        Write-Error "Failed to create function for pattern: $pattern_name"
        Write-Error $_.Exception.Message
    }
}

# Create YouTube function
function global:yt {
    param (
        [string]$video_link
    )

    try {
        $date_stamp = Get-Date -Format "yyyy-MM-dd"
        
        # Extract the video ID from the YouTube URL
        if ($video_link -match "v=([^&]+)") {
            $video_id = $matches[1]
        } else {
            Write-Error "Invalid YouTube URL: $video_link"
            return
        }

        # Construct the output path using the date and video ID
        $output_path = "$obsidian_base\$date_stamp-$video_id-youtube-transcript.md"

        & $fabricPath --youtube=$video_link --transcript -o $output_path

        if (Test-Path $output_path) {
            Write-Host "Created YouTube transcript: $output_path"
        } else {
            Write-Warning "YouTube transcript file was not created"
        }
    }
    catch {
        Write-Error "Failed to process YouTube video"
        Write-Error $_.Exception.Message
    }
}