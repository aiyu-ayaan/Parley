# Parley

Parley (formerly Whatsweb) is a linux desktop app that opens WhatsApp Web as an app. This app exists because WhatsApp has no native Linux app. The name avoids the WhatsApp trademark; "WhatsApp" only appears where it describes the service.

## Core Feature

- Light weight 
- Have ability to run on statup like whatsapp application work on the windows 
- Have option to have multiple whatsapp login at ones 
- Get the notification as same as Whatsapp app 


## How to make it 

The language we will use the golang and becaully it will spin the desktop app with discord like circular ui where user can add new profile (Multi account). It will spin the webview that
that will  open the web whatsapp. Make sure profile will get saved, so that user dont need to relogin all the time. 


## Security

All user data will be saved in Encripted form 

## Important Concideration

All will be as tinu it should be. 


## Git

Initilize the git if it's not and make proper commit at stage. 
Commit should follow a patter 

  Type(scope) : Commit message 


Type can be     : feat,fix,docs. (feel free to add any).

Scope can be    : ui,backend

Do not add AI co-author trailers to commits.

## Releases

Put a marker at the start of a commit subject on master to ask for a release:
`!fix`, `!feat`, `!major` (stable) or `!alpha`, `!beta`, `!stable` (channels),
e.g. `!feat(ui) : add account search`. `.github/workflows/release.yml` then opens
one release PR (version bump in wails.json + CHANGELOG). Merging it builds the
Linux binary and publishes the GitHub Release. Rules: `scripts/release`.


## For agents

Feel free to update Agents.md or any file anytime.
