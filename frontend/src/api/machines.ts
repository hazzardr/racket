import type { Machine } from "~/types";

const API_BASE = "/api";

export async function listMachines(): Promise<Machine[]> {
  const response = await fetch(`${API_BASE}/machines`);
  if (!response.ok) {
    throw new Error(`Failed to fetch machines: ${response.statusText}`);
  }
  return response.json();
}
