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
