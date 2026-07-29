import type { Part, PartInput, ImportResponse } from "~/types";

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

export async function importFromURL(url: string, machineId?: number): Promise<ImportResponse> {
  const query = machineId ? `?machine_id=${machineId}` : "";
  const response = await fetch(`${API_BASE}/parts/import-url${query}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ url }),
  });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    throw new Error(`Import failed: ${body?.error ?? response.statusText}`);
  }
  return response.json();
}

export async function importCSV(file: File, machineId?: number): Promise<ImportResponse> {
  const form = new FormData();
  form.append("file", file);
  const query = machineId ? `?machine_id=${machineId}` : "";
  const response = await fetch(`${API_BASE}/parts/import-csv${query}`, {
    method: "POST",
    body: form,
  });
  if (!response.ok) {
    const body = await response.json().catch(() => null);
    throw new Error(`Import failed: ${body?.error ?? response.statusText}`);
  }
  return response.json();
}
