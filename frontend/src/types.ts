export interface Machine {
  id: number;
  name: string;
  description: string | null;
  location: string | null;
  built_on: string | null;
  purchased_on: string | null;
}

export interface MachineInput {
  name: string;
  description?: string;
  location?: string;
  built_on?: string;
  purchased_on?: string;
}

export interface Part {
  id: number;
  machine_id: number | null;
  slot_id: number | null;
  name: string;
  category: string | null;
  description: string | null;
  manufacturer: string | null;
  model: string | null;
  serial_number: string | null;
  warranty_expires: string | null;
  purchased_on: string | null;
  purchased_from: string | null;
  purchase_price: number | null;
  guide_url: string | null;
}

export interface PartInput {
  machine_id?: number;
  name: string;
  category?: string;
  description?: string;
  manufacturer?: string;
  model?: string;
  serial_number?: string;
  warranty_expires?: string;
  purchased_on?: string;
  purchased_from?: string;
  purchase_price?: number;
  guide_url?: string;
  slot_id?: number;
}

export interface Slot {
  id: number;
  category: string;
  part: {
    id: number;
    name: string;
    model: string;
  } | null;
}

export interface MachineDetail {
  machine: Machine;
  parts: Part[];
  slots: Slot[];
}

export interface ImportResponse {
  imported: number;
  parts: Part[];
}
