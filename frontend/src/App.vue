<template>
  <div class="app">
    <header class="header">
      <div class="container header-inner">
        <RouterLink to="/" class="home">
          <div class="brand">
            <img src="/racket.svg" alt="" class="logo-icon" />
            <h1 class="logo">Racket</h1>
          </div>
          <p class="subtitle">Hardware Tracker</p>
        </RouterLink>
        <nav class="nav">
          <RouterLink to="/machines">Machines</RouterLink>
        </nav>
      </div>
    </header>

    <main class="container">
      <RouterView />
    </main>
  </div>
</template>

<style>
:root {
  color-scheme: dark;
  --color-bg: #0f1117;
  --color-surface: #1a1d27;
  --color-border: #2a2e3d;
  --color-text: #e4e6eb;
  --color-text-muted: #9ca0b0;
  --color-accent: #6ea8fe;
}

* {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

body {
  font-family: "Inter", system-ui, -apple-system, sans-serif;
  background: var(--color-bg);
  color: var(--color-text);
  line-height: 1.6;
}

.container {
  max-width: 1100px;
  margin: 0 auto;
  padding: 0 1.5rem;
}

.header {
  border-bottom: 1px solid var(--color-border);
  padding: 1.5rem 0;
  margin-bottom: 3rem;
}

.header-inner {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.home {
  text-decoration: none;
  color: inherit;
}

.brand {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  font-size: 1.75rem;
}

.logo-icon {
  width: 1em;
  height: 1em;
}

.logo {
  font-size: inherit;
  font-weight: 700;
  letter-spacing: -0.025em;
  color: var(--color-accent);
}

.subtitle {
  color: var(--color-text-muted);
  font-size: 0.875rem;
}

.nav a {
  color: var(--color-text-muted);
  text-decoration: none;
  font-size: 0.9375rem;
}

.nav a:hover {
  color: var(--color-text);
}

.nav a.router-link-active {
  color: var(--color-text);
}

.btn {
  display: inline-block;
  background: var(--color-accent);
  color: var(--color-bg);
  border: none;
  border-radius: 0.375rem;
  padding: 0.5rem 1rem;
  font-size: 0.9375rem;
  font-weight: 600;
  font-family: inherit;
  cursor: pointer;
  text-decoration: none;
}

.btn:hover {
  filter: brightness(1.1);
}

.btn:disabled {
  opacity: 0.6;
  cursor: default;
}

.btn-secondary {
  color: var(--color-text-muted);
  text-decoration: none;
  font-size: 0.9375rem;
  padding: 0.5rem 1rem;
}

.btn-secondary:hover {
  color: var(--color-text);
}

.form {
  max-width: 28rem;
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.form label {
  display: flex;
  flex-direction: column;
  gap: 0.375rem;
  font-size: 0.875rem;
  color: var(--color-text-muted);
}

.form input {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 0.375rem;
  padding: 0.5rem 0.75rem;
  color: var(--color-text);
  font-size: 0.9375rem;
  font-family: inherit;
}

.form input:focus {
  outline: none;
  border-color: var(--color-accent);
}

.form-actions {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-top: 0.5rem;
}
</style>
