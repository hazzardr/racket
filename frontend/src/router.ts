import { createRouter, createWebHistory } from "vue-router";
import LandingView from "./views/LandingView.vue";
import MachinesView from "./views/MachinesView.vue";
import NewMachineView from "./views/NewMachineView.vue";

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: "/", component: LandingView },
    { path: "/machines", component: MachinesView },
    { path: "/machines/new", component: NewMachineView },
  ],
});
