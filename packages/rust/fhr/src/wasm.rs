//! The fhr-wasm-1 exports' plumbing, used by [`crate::export_wasm!`]: buffers the host
//! writes requests into and reads responses from, each a leaked `Box<[u8]>`
//! until the host hands it back through `fhr_free`. Every request goes through
//! [`crate::call`].

use crate::Handler;

/// Pack a buffer for the host: `(ptr << 32) | len`, the buffer leaked until freed.
fn give(bytes: Vec<u8>) -> u64 {
    let b = bytes.into_boxed_slice();
    let len = b.len() as u64;
    let ptr = Box::into_raw(b) as *mut u8 as usize as u64;
    (ptr << 32) | len
}

/// `fhr_alloc`: a zeroed buffer of `size` bytes for the host to write into.
pub fn alloc(size: u32) -> u32 {
    (give(vec![0u8; size as usize]) >> 32) as u32
}

/// `fhr_free`: release a buffer from `fhr_alloc` or `fhr_call`.
///
/// # Safety
/// `ptr` and `size` must be exactly what `fhr_alloc` or `fhr_call` gave out,
/// and each buffer freed once.
pub unsafe fn free(ptr: u32, size: u32) {
    let slice = std::ptr::slice_from_raw_parts_mut(ptr as usize as *mut u8, size as usize);
    drop(Box::from_raw(slice));
}

/// `fhr_call`: answer the request in `[ptr, ptr + size)`.
///
/// # Safety
/// The range must be a buffer from `fhr_alloc`, which the host still owns.
pub unsafe fn call<H: Handler + ?Sized>(handler: &H, ptr: u32, size: u32) -> u64 {
    let request = std::slice::from_raw_parts(ptr as usize as *const u8, size as usize);
    give(crate::call(handler, request))
}
