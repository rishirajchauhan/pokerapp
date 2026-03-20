# Poker Prototype

A browser-based Texas Hold'em prototype built with Go on the backend and vanilla HTML, CSS, and JavaScript on the frontend. The server owns the game state, handles turn flow, and pushes live updates to connected players so you can open multiple tabs and play through a shared hand locally.

## Overview

This project is a practical full-stack learning build focused on:

- game state management
- multiplayer interaction across browser tabs
- server-driven poker logic
- clean Git and GitHub project history

It is currently a local multiplayer prototype rather than a production-ready poker platform.

## Tech Stack

- Go 1.26
- `net/http` for the web server and API endpoints
- Server-Sent Events for real-time table updates
- Vanilla JavaScript for client-side interaction
- HTML and CSS for the interface

## Current Features

- Join a shared table with a player name
- Support up to 6 players
- Start a hand once enough players have joined
- Deal hole cards and community cards
- Rotate dealer and manage turn order
- Track chips, pot size, and current bet
- Allow core actions: `start`, `check`, `call`, and `fold`
- Stream live state updates to all connected clients
- Persist the current player ID in the browser with `localStorage`
- Keep poker state authoritative on the server

## Project Structure

```text
.
|-- main.go
|-- go.mod
|-- static/
|   |-- index.html
|   |-- styles.css
|   `-- app.js
```

## Run Locally

Make sure Go 1.26 or newer is installed, then start the server:

```bash
go run .
```

Open [http://localhost:8080](http://localhost:8080) in two or more browser tabs to simulate multiple players joining the same table.

## How It Works

- The Go server exposes REST-style endpoints for joining, starting a hand, taking actions, and reading table state.
- Real-time updates are delivered through Server-Sent Events so each connected client stays in sync.
- The frontend renders table state, player cards, betting info, and recent actions from server responses.
- The browser only acts as a client; game rules and table state remain controlled by the backend.

## Current Limitations

This version is intentionally lightweight and still missing several poker and production features:

- no raise or bet action yet
- no side pot or all-in handling
- no private rooms or matchmaking
- no authentication
- no database or saved game history
- no reconnect recovery for dropped sessions
- limited test coverage
- not hardened for production deployment

## Why This Project Matters

This repo is being used to practice:

- backend API development in Go
- real-time multiplayer state synchronization
- poker game logic
- frontend rendering without a framework
- writing meaningful commits and maintaining a clean GitHub project

## Next Improvements

- add `bet`, `raise`, and `all-in` actions
- extract poker logic into dedicated packages
- add unit tests for hand flow and state transitions
- improve hand evaluation and winner resolution
- support room codes or separate tables
- add reconnect handling and better session management
- polish the UI and game feedback

## Status

Active work in progress. The current goal is to turn a working local prototype into a cleaner, more complete multiplayer poker app with better gameplay coverage and stronger testability.
