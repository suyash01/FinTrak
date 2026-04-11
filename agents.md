# 🤖 Agents Guide

Welcome, AI Agent! This document is designed to help you quickly understand, maintain, and extend the **FinTrak** codebase.

## 🧠 Contextual Overview

FinTrak is a personal finance tracker with a clear separation of concerns:
- **Backend (Go)**: A RESTful API built with Gin and PostgreSQL. Focuses on data integrity, transaction linking logic, and rule execution.
- **Frontend (React)**: A modern UI built with Vite and Tailwind CSS 4. Focuses on data visualization and high-density transaction management.

## 🛠️ Developmental Guidelines

### For Backend Agents (Go)
1. **Migrations First**: Never modify the database schema directly. Always create a new migration in `backend/db/migrations`.
2. **Handlers & Logic**: Keep `handlers/` thin. Business logic (like transfer detection) should reside in dedicated utility functions or model methods where appropriate.
3. **Error Handling**: Follow standard Go patterns. Return structured JSON errors from handlers.
4. **Environment**: Use `.env` for configuration. The `config/` package handles loading these values.

### For Frontend Agents (React)
1. **Component Purity**: Prefix UI components with their functional responsibility. Use Tailwind CSS 4 for all styling.
2. **State Management**: Use React Context for global state (e.g., `SettingsContext` for compact layout).
3. **Data Fetching**: Consolidate API calls in `src/api/`. Do not hardcode URLs in components.
4. **Responsiveness**: Ensure all new components support the **Compact Layout** toggle.

## 🔄 Common Task Patterns

### Adding a New API Endpoint
1. Define the model in `models/`.
2. Create the handler in `handlers/`.
3. Register the route in `main.go`.
4. (If needed) Create a migration in `db/migrations`.

### Creating a New UI Dashboard Widget
1. Create the component in `components/dashboard/`.
2. Fetch required data in `src/api/dashboard.js`.
3. Use **Recharts** for visualizations.
4. Integrate into `src/App.jsx` or the main Dashboard page.

## 🧭 Navigating the Code
- **Rules Logic**: Check `backend/handlers/rules.go` and `backend/models/rule.go`.
- **Linking Logic**: Look at `backend/handlers/links.go` for transfer/cashback suggestions.
- **Import Logic**: See `frontend/src/components/transactions/ImportModal.jsx` for client-side parsing.

---

*Remember: This codebase values performance and clarity. Keep it lean!*
