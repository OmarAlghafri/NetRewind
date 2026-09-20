//! The local AI assistant's desktop-shell half: sidecar runtime lifecycle,
//! model storage, and the staged CLI (Part C of the 1.2.0 plan). Off by
//! default (`AI_FEATURE_ENABLED`) until a model profile passes its
//! evaluation gate - see docs/product/adr/0006.

pub mod cli;
pub mod codes;
pub mod models;
pub mod runtime;
