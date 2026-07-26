import type { Machine, MachineInput } from "~/types";

const API_BASE = "/api";

export async function listMachines(): Promise<Machine[]> {
  const response = await fetch(`${API_BASE}/machines`);
  if (!response.ok) {
    throw new Error(`Failed to fetch machines: ${response.statusText}`);
  }
  return response.json();
}

export async function createMachine(input: MachineInput): Promise<Machine> {
  const response = await fetch(`${API_BASE}/machines`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new Error(`Failed to create machine: ${response.statusText}`);
  }
  return response.json();
}
