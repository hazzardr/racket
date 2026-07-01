export interface Machine {
  id: number;
  name: string;
  description: string | null;
  location: string | null;
  built_on: string | null;
  purchased_on: string | null;
}
