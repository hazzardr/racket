import type { Part, PartInput } from "~/types";

const API_BASE = "/api";

export async function createPart(input: PartInput): Promise<Part> {
  const response = await fetch(`${API_BASE}/parts`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!response.ok) {
    throw new Error(`Failed to create part: ${response.statusText}`);
  }
  return response.json();
}

export async function deletePart(id: number): Promise<void> {
  const response = await fetch(`${API_BASE}/parts/${id}`, {
    method: "DELETE",
  });
  if (!response.ok) {
    throw new Error(`Failed to delete part: ${response.statusText}`);
  }
}
