# Distributed Rate Limiter (High-Performance)

A production-grade, distributed rate-limiting service built in **Go** and **PostgreSQL**. This project serves as a bridge, applying low-level systems engineering principles (concurrency, atomics, and high-throughput networking) to a modern backend stack.

## 🚀 Overview
In high-traffic environments like e-commerce, protecting downstream services from being overwhelmed is critical. This project implements a distributed "Token Bucket" algorithm capable of handling thousands of checks per second with sub-millisecond latency.

## 🛠 Tech Stack
- **Language:** Go (Golang)
- **Database:** PostgreSQL (State persistence & configuration)
- **Communication:** gRPC / Protocol Buffers
- **Infrastructure:** Docker & Kubernetes (Helm)

## 📖 Project Documentation
- [**Design Criteria & Requirements**](./criteria.md): Detailed functional and non-functional requirements.
- [**Development Roadmap**](./roadmap.md): The step-by-step execution plan.
- [**Architecture & Benchmarks**](#): (Coming soon) Detailed breakdown of the internal logic and performance results.

## 🧠 Why This Project?
While my background is in **C++ systems and infrastructure**—building distributed backtest systems and HA config servers—this project demonstrates my ability to solve the same scaling challenges using **Go** and **Relational Databases**.