import type { Slot } from "~/types";

const API_BASE = "/api";

export async function createSlot(machineId: number, category: string): Promise<Slot> {
  const response = await fetch(`${API_BASE}/machines/${machineId}/slots`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ category }),
  });
  if (!response.ok) {
    throw new Error(`Failed to create slot: ${response.statusText}`);
  }
  return response.json();
}

export async function deleteSlot(slotId: number): Promise<void> {
  const response = await fetch(`${API_BASE}/slots/${slotId}`, {
    method: "DELETE",
  });
  if (!response.ok) {
    throw new Error(`Failed to delete slot: ${response.statusText}`);
  }
}
