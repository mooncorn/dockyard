# Dockyard

Dockyard is a self-hosted Docker control plane that lets anyone turn any machine into a secure, managed node. Users deploy containers via a web UI, view live logs in real time, and control services (start/stop/delete) — all with fine-grained permissions. It runs entirely on your infrastructure, requires no cloud, and gives you full sovereignty over your Docker fleet.

## Project Structure
This repository contains three interconnected projects:
- **console/**: React/TypeScript web dashboard
- **control/**: Go-based central orchestrator
- **worker/**: Go-based task execution worker

## Key Files
- `proto/agent.proto`: Source of truth for all message types