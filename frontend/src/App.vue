<script setup lang="ts">
import { onMounted, ref } from "vue";
import { listMachines } from "~/api/machines";
import type { Machine } from "~/types";

const machines = ref<Machine[]>([]);
const loading = ref(true);
const error = ref<string | null>(null);

onMounted(async () => {
    try {
        machines.value = await listMachines();
    } catch (err) {
        error.value = err instanceof Error ? err.message : "Unknown error";
    } finally {
        loading.value = false;
    }
});
</script>

<template>
    <main class="container">
        <h1>Machines</h1>
        <div v-if="loading">Loading...</div>
        <div v-else-if="error" class="error">{{ error }}</div>
        <ul v-else class="machine-list">
            <li
                v-for="machine in machines"
                :key="machine.id"
                class="machine-item"
            >
                <h2>{{ machine.name }}</h2>
                <p v-if="machine.description">{{ machine.description }}</p>
                <p v-if="machine.location" class="meta">
                    Location: {{ machine.location }}
                </p>
            </li>
        </ul>
    </main>
</template>

<style scoped>
.container {
    max-width: 800px;
    margin: 0 auto;
    padding: 2rem;
    font-family:
        system-ui,
        -apple-system,
        BlinkMacSystemFont,
        "Segoe UI",
        Roboto,
        sans-serif;
}

h1 {
    margin-bottom: 1.5rem;
}

.machine-list {
    list-style: none;
    padding: 0;
    margin: 0;
}

.machine-item {
    padding: 1rem;
    margin-bottom: 0.75rem;
    border: 1px solid #e5e7eb;
    border-radius: 0.5rem;
}

.machine-item h2 {
    margin: 0 0 0.5rem;
    font-size: 1.25rem;
}

.machine-item p {
    margin: 0.25rem 0;
    color: #4b5563;
}

.meta {
    font-size: 0.875rem;
    color: #6b7280;
}

.error {
    color: #dc2626;
}
</style>
